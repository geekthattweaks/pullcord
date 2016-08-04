package monitor

import (
	"github.com/fitstar/falcore"
	"github.com/stretchr/testify/assert"
	"github.com/stuphlabs/pullcord"
	"testing"
	"time"
)

// serveLandingPage is a testing helper function that creates a webserver that
// other tests for MinSession can use to verify monitoring service.
func serveLandingPage(landingServer *falcore.Server) {
	err := landingServer.ListenAndServe()
	if err != nil {
		panic(err)
	}
}

// TestMinMonitorUpService verifies that a MinMonitor generated by
// NewMinMonitor will give the expected status for a service that is up.
func TestMinMonitorUpService(t *testing.T) {
	testServiceName := "test"
	testHost := "localhost"
	testPort := uint16(58080)
	testProtocol := "tcp"
	gracePeriod := time.Duration(0)
	deferProbe := true
	serverStartupDuration, err := time.ParseDuration("1s")
	assert.NoError(t, err)

	landingPipeline := falcore.NewPipeline()
	landingPipeline.Upstream.PushBack(pullcord.NewLandingFilter())
	landingServer := falcore.NewServer(int(testPort), landingPipeline)
	go serveLandingPage(landingServer)
	defer landingServer.StopAccepting()

	mon := NewMinMonitor()
	err = mon.Add(
		testServiceName,
		testHost,
		testPort,
		testProtocol,
		gracePeriod,
		deferProbe,
	)
	assert.NoError(t, err)

	time.Sleep(serverStartupDuration)

	up, err := mon.Status(testServiceName)
	assert.NoError(t, err)
	assert.True(t, up)
}
