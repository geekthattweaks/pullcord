package authentication

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/proidiot/gone/log"
	"github.com/stuphlabs/pullcord/config"
	"github.com/stuphlabs/pullcord/util"
)

// XSRFTokenLength is the length of XSRF token strings.
const XSRFTokenLength = 64

const msgInvalidCredentials = "Invalid Credentials"

// LoginHandler is a login handling system that presents a login page backed by
// a PasswordChecker for users that are not yet logged in, while seamlessly
// forwarding all requests downstream for users that are logged in. A
// LoginHandler has an identifier (which it uses to differentiate its login
// tokens and authentication flags from other components, possibly including
// other LoginHandlers), a PasswordChecker (which it allows users to
// authenticate against in conjunction with its own XSRF token), and a
// downstream RequestFilter (possibly an entire pipeline).
type LoginHandler struct {
	Identifier      string
	PasswordChecker PasswordChecker
	Downstream      http.Handler
}

func init() {
	config.MustRegisterResourceType(
		"loginhandler",
		func() json.Unmarshaler {
			return new(LoginHandler)
		},
	)
}

// UnmarshalJSON implements encoding/json.Unmarshaler.
func (h *LoginHandler) UnmarshalJSON(input []byte) error {
	var t struct {
		Identifier      string
		PasswordChecker config.Resource
		Downstream      config.Resource
	}

	dec := json.NewDecoder(bytes.NewReader(input))
	if e := dec.Decode(&t); e != nil {
		_ = log.Err("Unable to decode LoginHandler")
		return e
	}

	p := t.PasswordChecker.Unmarshalled
	switch p := p.(type) {
	case PasswordChecker:
		h.PasswordChecker = p
	default:
		_ = log.Err(
			fmt.Sprintf(
				"Registry value is not a PasswordChecker: %#v",
				t.PasswordChecker,
			),
		)
		return config.UnexpectedResourceType
	}

	if d, ok := t.Downstream.Unmarshalled.(http.Handler); ok {
		h.Downstream = d
	} else {
		_ = log.Err(
			fmt.Sprintf(
				"Registry value is not a RequestFilter: %#v",
				t.Downstream,
			),
		)
		return config.UnexpectedResourceType
	}

	h.Identifier = t.Identifier

	return nil
}

func (h *LoginHandler) ServeHTTP(
	w http.ResponseWriter,
	request *http.Request,
) {
	errString := ""
	rawsesh := request.Context().Value(ctxKeySession)
	if rawsesh == nil {
		_ = log.Crit(
			"login handler was unable to retrieve session from" +
				" context",
		)
		util.InternalServerError.ServeHTTP(w, request)
		return
	}
	sesh := rawsesh.(Session)

	authSeshKey := "authenticated-" + h.Identifier
	xsrfKey := "xsrf-" + h.Identifier
	usernameKey := "username-" + h.Identifier
	passwordKey := "password-" + h.Identifier

	if authd, err := sesh.GetValue(
		authSeshKey,
	); err == nil && authd == true {
		_ = log.Debug("login handler passing request along")
		h.Downstream.ServeHTTP(w, request)
		return
	} else if err != NoSuchSessionValueError {
		_ = log.Err(
			fmt.Sprintf(
				"login handler error during auth status"+
					" retrieval: %v",
				err,
			),
		)
		util.InternalServerError.ServeHTTP(w, request)
		return
	}

	if xsrfStored, err := sesh.GetValue(
		xsrfKey,
	); err != nil && err != NoSuchSessionValueError {
		_ = log.Err(
			fmt.Sprintf(
				"login handler error during xsrf token"+
					" retrieval: %v",
				err,
			),
		)
		util.InternalServerError.ServeHTTP(w, request)
		return
	} else if err == NoSuchSessionValueError {
		_ = log.Info("login handler received new request")
	} else if err = request.ParseForm(); err != nil {
		err = log.Warning(
			fmt.Sprintf(
				"login handler error during ParseForm: %#v",
				err,
			),
		)
		if err != nil {
			// this is too suspicious
			util.Forbidden.ServeHTTP(w, request)
			return
		}
		errString = "Bad request"
	} else if xsrfRcvd, present :=
		request.PostForm[xsrfKey]; !present {
		_ = log.Info("login handler did not receive xsrf token")
		errString = msgInvalidCredentials
	} else if len(xsrfRcvd) != 1 || 1 != subtle.ConstantTimeCompare(
		[]byte(xsrfStored.(string)),
		[]byte(xsrfRcvd[0]),
	) {
		_ = log.Info("login handler received bad xsrf token")
		errString = msgInvalidCredentials
	} else if uVals, present :=
		request.PostForm[usernameKey]; !present {
		_ = log.Info("login handler did not receive username")
		errString = msgInvalidCredentials
	} else if pVals, present :=
		request.PostForm[passwordKey]; !present {
		_ = log.Info("login handler did not receive password")
		errString = msgInvalidCredentials
	} else if len(uVals) != 1 || len(pVals) != 1 {
		_ = log.Info(
			"login handler received multi values for username or" +
				" password",
		)
		errString = "Bad request"
	} else if err = h.PasswordChecker.CheckPassword(
		uVals[0],
		pVals[0],
	); err == NoSuchIdentifierError {
		_ = log.Info("login handler received bad username")
		errString = msgInvalidCredentials
	} else if err == BadPasswordError {
		_ = log.Info("login handler received bad password")
		errString = msgInvalidCredentials
	} else if err != nil {
		_ = log.Err(
			fmt.Sprintf(
				"login handler error during CheckPassword: %#v",
				err,
			),
		)
		util.InternalServerError.ServeHTTP(w, request)
		return
	} else if err = sesh.SetValue(authSeshKey, true); err != nil {
		_ = log.Err(
			fmt.Sprintf(
				"login handler error during auth set: %#v",
				err,
			),
		)
		util.InternalServerError.ServeHTTP(w, request)
		return
	} else {
		err = log.Notice(
			fmt.Sprintf(
				"login successful for: %s",
				uVals[0],
			),
		)
		if err != nil {
			util.InternalServerError.ServeHTTP(w, request)
			return
		}
		h.Downstream.ServeHTTP(w, request)
		return
	}

	rawXSRFToken := make([]byte, XSRFTokenLength)
	if rsize, err := rand.Read(
		rawXSRFToken[:],
	); err != nil || rsize != XSRFTokenLength {
		_ = log.Err(
			fmt.Sprintf(
				"login handler error during xsrf generation:"+
					" len expected: %d, actual: %d,"+
					" err: %#v",
				XSRFTokenLength,
				rsize,
				err,
			),
		)
		util.InternalServerError.ServeHTTP(w, request)
		return
	}
	nextXSRFToken := hex.EncodeToString(rawXSRFToken)

	if err := sesh.SetValue(xsrfKey, nextXSRFToken); err != nil {
		_ = log.Err(
			fmt.Sprintf(
				"login handler error during xsrf set: %#v",
				err,
			),
		)
		util.InternalServerError.ServeHTTP(w, request)
		return
	}

	errMarkup := ""
	if errString != "" {
		errMarkup = fmt.Sprintf(
			"<label class=\"error\">%s</label><br />",
			errString,
		)
	}

	_, err := fmt.Fprintf(
		w,
		"<html><head><title>Pullcord Login</title></head><body>"+
			"<form method=\"POST\" action=\"%s\"><fieldset>"+
			"<legend>Pullcord Login</legend>%s"+
			"<label for=\"username\">Username:</label>"+
			"<input type=\"text\" name=\"%s\" id=\"username\" />"+
			"<label for=\"password\">Password:</label>"+
			"<input type=\"password\" name=\"%s\""+
			"id=\"password\" /><input type=\"hidden\" name=\"%s\""+
			" value=\"%s\" /><input type=\"submit\""+
			" value=\"Login\"/></fieldset></form></body></html>",
		request.URL.Path,
		errMarkup,
		usernameKey,
		passwordKey,
		xsrfKey,
		nextXSRFToken,
	)
	if err != nil {
		_ = log.Error(
			fmt.Sprintf(
				"Unable to write login form: %s",
				err.Error(),
			),
		)
		util.InternalServerError.ServeHTTP(w, request)
	}
	return
}
