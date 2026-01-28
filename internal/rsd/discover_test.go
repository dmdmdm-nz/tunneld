package rsd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetInterfaceByName_NotFound(t *testing.T) {
	// Try to get an interface that doesn't exist
	_, err := GetInterfaceByName("nonexistent_interface_xyz")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "interface not found")
}

func TestGetInterfaceByName_LoopbackExists(t *testing.T) {
	// The loopback interface should exist on all systems
	iface, err := GetInterfaceByName("lo0")
	if err != nil {
		// On Linux it might be "lo" instead of "lo0"
		iface, err = GetInterfaceByName("lo")
	}

	// If neither exists (unusual), skip the test
	if err != nil {
		t.Skip("loopback interface not found")
	}

	assert.NotNil(t, iface)
	// Loopback should always be up
	assert.NotEmpty(t, iface.Name)
}

func TestRSD_PORT_Constant(t *testing.T) {
	// RSD port should be 58783 as per Apple's documentation
	assert.Equal(t, 58783, RSD_PORT)
}
