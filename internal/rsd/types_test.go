package rsd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEventType_Constants(t *testing.T) {
	assert.Equal(t, EventType("SERVICE_ADDED"), RsdServiceAdded)
	assert.Equal(t, EventType("SERVICE_REMOVED"), RsdServiceRemoved)
}

func TestRsdService_Fields(t *testing.T) {
	svc := RsdService{
		Udid:             "00008030-000000000000002E",
		InterfaceName:    "en5",
		Address:          "fe80::1%5",
		DeviceIosVersion: "17.0",
	}

	assert.Equal(t, "00008030-000000000000002E", svc.Udid)
	assert.Equal(t, "en5", svc.InterfaceName)
	assert.Equal(t, "fe80::1%5", svc.Address)
	assert.Equal(t, "17.0", svc.DeviceIosVersion)
}

func TestRsdServiceEvent_Fields(t *testing.T) {
	svc := RsdService{
		Udid:             "test-udid",
		InterfaceName:    "en0",
		Address:          "fe80::1%1",
		DeviceIosVersion: "18.0",
	}

	event := RsdServiceEvent{
		Type: RsdServiceAdded,
		Info: svc,
	}

	assert.Equal(t, RsdServiceAdded, event.Type)
	assert.Equal(t, svc, event.Info)
}

func TestEventHandler_Type(t *testing.T) {
	var called bool
	var receivedEvent RsdServiceEvent

	handler := EventHandler(func(event RsdServiceEvent) {
		called = true
		receivedEvent = event
	})

	testEvent := RsdServiceEvent{
		Type: RsdServiceRemoved,
		Info: RsdService{Udid: "test-udid"},
	}
	handler(testEvent)

	assert.True(t, called)
	assert.Equal(t, testEvent, receivedEvent)
}
