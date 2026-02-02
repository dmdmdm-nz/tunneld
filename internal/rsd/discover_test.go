package rsd

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

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

func TestFindRsdService_SuspendCalledOnValidInterface(t *testing.T) {
	// Save original and restore after test
	originalSuspendFunc := suspendRemotedFunc
	defer func() { suspendRemotedFunc = originalSuspendFunc }()

	// Track suspend and resume calls
	var suspendCalled atomic.Bool
	var resumeCalled atomic.Bool

	// Mock suspendRemotedFunc to track calls
	suspendRemotedFunc = func() (func(), error) {
		suspendCalled.Store(true)
		return func() {
			resumeCalled.Store(true)
		}, nil
	}

	// Create a context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Use loopback interface which should exist
	interfaceName := "lo0"
	if _, err := GetInterfaceByName(interfaceName); err != nil {
		interfaceName = "lo"
		if _, err := GetInterfaceByName(interfaceName); err != nil {
			t.Skip("loopback interface not found")
		}
	}

	// Start FindRsdService - it should call suspend because interface exists
	_, _ = FindRsdService(ctx, interfaceName)

	// Verify suspend was called (interface exists, so Browse goroutine started)
	assert.True(t, suspendCalled.Load(), "suspendRemoted should be called when interface exists")

	// Give the goroutine time to complete and call resume
	time.Sleep(50 * time.Millisecond)
	assert.True(t, resumeCalled.Load(), "resumeRemoted should be called after Browse completes")
}

func TestFindRsdService_NoSuspendOnInterfaceNotFound(t *testing.T) {
	// Save original and restore after test
	originalSuspendFunc := suspendRemotedFunc
	defer func() { suspendRemotedFunc = originalSuspendFunc }()

	// Track if suspend was called
	var suspendCalled atomic.Bool

	// Mock suspendRemotedFunc
	suspendRemotedFunc = func() (func(), error) {
		suspendCalled.Store(true)
		return func() {}, nil
	}

	ctx := context.Background()

	// Use a non-existent interface - should fail on first GetInterfaceByName call
	_, err := FindRsdService(ctx, "nonexistent_test_iface_xyz")

	// Should get an error about interface not found
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "interface not found")

	// Verify suspend was NOT called - we fail early before starting the Browse goroutine
	assert.False(t, suspendCalled.Load(), "suspendRemoted should not be called when interface is not found")
}
