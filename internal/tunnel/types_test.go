package tunnel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTunnelStatus_String(t *testing.T) {
	tests := []struct {
		status   TunnelStatus
		expected string
	}{
		{Disconnected, "Disconnected"},
		{ConnectingToDevice, "ConnectingToDevice"},
		{VerifyingPairing, "VerifyingPairing"},
		{Pairing, "Pairing"},
		{Paired, "ConnectingToTunnel"},
		{Failed, "Failed"},
		{Connected, "Connected"},
		{TunnelStatus(100), "Unknown"},
		{TunnelStatus(-1), "Unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.status.String())
		})
	}
}

func TestTunnelStatus_IotaValues(t *testing.T) {
	// Verify iota values are as expected
	assert.Equal(t, TunnelStatus(0), Disconnected)
	assert.Equal(t, TunnelStatus(1), ConnectingToDevice)
	assert.Equal(t, TunnelStatus(2), VerifyingPairing)
	assert.Equal(t, TunnelStatus(3), Pairing)
	assert.Equal(t, TunnelStatus(4), Paired)
	assert.Equal(t, TunnelStatus(5), Failed)
	assert.Equal(t, TunnelStatus(6), Connected)
}

func TestTunnelEventType_Constants(t *testing.T) {
	assert.Equal(t, TunnelEventType("DEVICE_NOT_PAIRED"), DeviceNotPaired)
	assert.Equal(t, TunnelEventType("DEVICE_PAIRED"), DevicePaired)
	assert.Equal(t, TunnelEventType("TUNNEL_PROGRESS"), TunnelProgress)
}

func TestTunnelEvent_Fields(t *testing.T) {
	event := TunnelEvent{
		Type:   DevicePaired,
		Udid:   "00008030-000000000000002E",
		Status: Connected,
	}

	assert.Equal(t, DevicePaired, event.Type)
	assert.Equal(t, "00008030-000000000000002E", event.Udid)
	assert.Equal(t, Connected, event.Status)
}

func TestRsdServiceInfo_Fields(t *testing.T) {
	info := RsdServiceInfo{
		Name: "com.apple.webinspector",
		Port: 62078,
	}

	assert.Equal(t, "com.apple.webinspector", info.Name)
	assert.Equal(t, uint32(62078), info.Port)
}
