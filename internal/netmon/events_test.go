package netmon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEventType_Constants(t *testing.T) {
	assert.Equal(t, EventType("INTERFACE_ADDED"), InterfaceAdded)
	assert.Equal(t, EventType("INTERFACE_REMOVED"), InterfaceRemoved)
}

func TestInterfaceEvent_Fields(t *testing.T) {
	event := InterfaceEvent{
		Type:          InterfaceAdded,
		InterfaceName: "en0",
	}

	assert.Equal(t, InterfaceAdded, event.Type)
	assert.Equal(t, "en0", event.InterfaceName)
}

func TestEventHandler_Type(t *testing.T) {
	var called bool
	var receivedEvent InterfaceEvent

	handler := EventHandler(func(event InterfaceEvent) {
		called = true
		receivedEvent = event
	})

	testEvent := InterfaceEvent{
		Type:          InterfaceRemoved,
		InterfaceName: "en1",
	}
	handler(testEvent)

	assert.True(t, called)
	assert.Equal(t, testEvent, receivedEvent)
}
