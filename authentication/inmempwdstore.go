package authentication

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"

	"github.com/proidiot/gone/errors"
	"github.com/stuphlabs/pullcord/config"

	"golang.org/x/crypto/pbkdf2"
)

func init() {
	config.MustRegisterResourceType(
		"inmempwdstore",
		func() json.Unmarshaler {
			return new(InMemPwdStore)
		},
	)
}

// Pbkdf2KeyLength is the length (in bytes) of the generated PBKDF2 hashes.
const Pbkdf2KeyLength = 64

// Pbkdf2MinIterations is the minimum number of iterations allowed for PBKDF2
// hashes.
const Pbkdf2MinIterations = uint16(4096)

// InsufficientIterationsError is the error object that is returned if the
// requested number of iterations for a new PBKDF2 hash is less than
// Pbkdf2MinIterations.
const InsufficientIterationsError = errors.New(
	"The number of iterations must be at least Pbkdf2MinIterations",
)

// InsufficientEntropyError is the error object that is returned if the
// operating system does not have enough entropy to generated a random salt of
// length Pbkdf2KeyLength.
const InsufficientEntropyError = errors.New(
	"The amount of entropy available from the operating system was not" +
		" enough to generate a salt of length Pbkdf2KeyLength",
)

// NoSuchIdentifierError is the error object that is returned if the given
// identifier (probably a username) does not exist in the password store.
//
// It is considered best practice to not indicate to a possible attacker
// whether an authentication attempt failed due to a bad password or due to
// a non-existent user. However, while this implementation makes a few very
// modest attempts to reduce time-based information leakage, the way the
// identifier lookup process is implemented is likely to leak information about
// the presence of a user. Perhaps that issue will be fixed at a later time,
// but it is worth at least knowing for the time being.
const NoSuchIdentifierError = errors.New(
	"The given identifier does not have an entry in the password store",
)

// BadPasswordError is the error object that is returned if the given
// identifier (probably a username) does exist in the password store, but the
// given password does not generate a matching hash.
const BadPasswordError = errors.New(
	"The hash generated from the given password does not match the hash" +
		" associated with the given identifier in the password store",
)

// IncorrectSaltLengthError is the error object that is returned if the given
// base64 encoded salt does not decode to exactly Pbkdf2KeyLength bytes.
const IncorrectSaltLengthError = errors.New(
	"The base64 encoded salt does not decode to Pbkdf2KeyLength bytes",
)

// IncorrectHashLengthError is the error object that is returned if the given
// base64 encoded hash does not decode to exactly Pbkdf2KeyLength bytes.
const IncorrectHashLengthError = errors.New(
	"The base64 encoded hash does not decode to Pbkdf2KeyLength bytes",
)

// Pbkdf2Hash is a cryptogaphic hash generated by PBKDF2 using SHA-256 for
// an InMemPwdStore. The iteration count must be at least Pbkdf2MinIterations
// to be accepted by this implementation. The hash and salt must be standard
// base64 encoded (i.e. RFC 4648 with padding) byte arrays of length
// Pbkdf2KeyLength.
type Pbkdf2Hash struct {
	Hash       [Pbkdf2KeyLength]byte
	Salt       [Pbkdf2KeyLength]byte
	Iterations uint16
}

// UnmarshalJSON implements encoding/json.Unmarshaler.
func (hashStruct *Pbkdf2Hash) UnmarshalJSON(input []byte) error {
	var t struct {
		Hash       string
		Salt       string
		Iterations uint16
	}

	dec := json.NewDecoder(bytes.NewReader(input))
	if e := dec.Decode(&t); e != nil {
		return e
	} else if h, e := base64.StdEncoding.DecodeString(t.Hash); e != nil {
		return e
	} else if len(h) != Pbkdf2KeyLength {
		return IncorrectHashLengthError
	} else if s, e := base64.StdEncoding.DecodeString(t.Salt); e != nil {
		return e
	} else if len(s) != Pbkdf2KeyLength {
		return IncorrectSaltLengthError
	} else if t.Iterations < Pbkdf2MinIterations {
		return InsufficientIterationsError
	} else {
		subtle.ConstantTimeCopy(1, hashStruct.Hash[:], h)
		subtle.ConstantTimeCopy(1, hashStruct.Salt[:], s)
		hashStruct.Iterations = t.Iterations
		return nil
	}
}

// MarshalJSON implements encoding/json.Marshaler.
func (hashStruct *Pbkdf2Hash) MarshalJSON() ([]byte, error) {
	var t struct {
		Hash       string
		Salt       string
		Iterations uint16
	}

	t.Hash = base64.StdEncoding.EncodeToString(hashStruct.Hash[:])
	t.Salt = base64.StdEncoding.EncodeToString(hashStruct.Salt[:])
	t.Iterations = hashStruct.Iterations

	return json.Marshal(t)
}

// InMemPwdStore is a basic password store where all the identifiers and hash
// information are stored in memory. This would likely not be a useful password
// store implementation in a production environment, but it can be useful in
// testing. All passwords are hashed using PBKDF2 with SHA-256.
type InMemPwdStore map[string]*Pbkdf2Hash

// GetPbkdf2Hash generates a new PBKDF2 hash in a secure way from a raw
// password and an iteration count.
func GetPbkdf2Hash(
	password string,
	iterations uint16,
) (*Pbkdf2Hash, error) {
	if iterations < Pbkdf2MinIterations {
		return nil, InsufficientIterationsError
	}

	var hashStruct Pbkdf2Hash
	randCount, err := rand.Read(hashStruct.Salt[:])
	if err != nil {
		return nil, err
	} else if randCount != Pbkdf2KeyLength {
		return nil, InsufficientEntropyError
	}

	hashStruct.Iterations = iterations

	subtle.ConstantTimeCopy(1, hashStruct.Hash[:], pbkdf2.Key(
		[]byte(password),
		hashStruct.Salt[:],
		int(hashStruct.Iterations),
		Pbkdf2KeyLength,
		sha256.New,
	))

	return &hashStruct, nil
}

// Check verifies that the given password yields the same PBKDF2 hash given the
// same salt and iteration count. It returns nil if the resulting hash matches,
// or an error if the resulting hash does not match.
func (hashStruct *Pbkdf2Hash) Check(
	password string,
) error {
	genHash := pbkdf2.Key(
		[]byte(password),
		hashStruct.Salt[:],
		int(hashStruct.Iterations),
		Pbkdf2KeyLength,
		sha256.New,
	)

	if 1 == subtle.ConstantTimeCompare(hashStruct.Hash[:], genHash) {
		return nil
	}

	return BadPasswordError
}

// CheckPassword implements the required password checking function to make
// InMemPwdStore a PasswordChecker implementation.
func (store *InMemPwdStore) CheckPassword(id, pass string) error {
	hs, present := (map[string]*Pbkdf2Hash(*store))[id]
	if !present {
		return NoSuchIdentifierError
	}

	return hs.Check(pass)
}

// UnmarshalJSON implements encoding/json.Unmarshaler.
func (store *InMemPwdStore) UnmarshalJSON(input []byte) error {
	return json.Unmarshal(input, (*map[string]*Pbkdf2Hash)(store))
}
