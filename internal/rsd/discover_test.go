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

func TestFindRsdService_ResumeRemotedCalledOnCancel(t *testing.T) {
	// Save original and restore after test
	originalSuspendFunc := suspendRemotedFunc
	defer func() { suspendRemotedFunc = originalSuspendFunc }()

	// Track if resume was called
	var resumeCalled atomic.Bool

	// Mock suspendRemotedFunc to return a trackable resume function
	suspendRemotedFunc = func() (func(), error) {
		return func() {
			resumeCalled.Store(true)
		}, nil
	}

	// Create a context that we'll cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Start FindRsdService in a goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Use a non-existent interface so it fails quickly on GetInterfaceByName
		// or times out on discovery - either way, we'll cancel first
		_, _ = FindRsdService(ctx, "nonexistent_test_iface")
	}()

	// Give it a moment to start, then cancel
	time.Sleep(10 * time.Millisecond)
	cancel()

	// Wait for FindRsdService to return
	select {
	case <-done:
		// Good, it returned
	case <-time.After(5 * time.Second):
		t.Fatal("FindRsdService did not return after context cancellation")
	}

	// Verify resume was called
	assert.True(t, resumeCalled.Load(), "resumeRemoted should be called when context is cancelled")
}

func TestFindRsdService_ResumeRemotedCalledOnInterfaceNotFound(t *testing.T) {
	// Save original and restore after test
	originalSuspendFunc := suspendRemotedFunc
	defer func() { suspendRemotedFunc = originalSuspendFunc }()

	// Track if resume was called
	var resumeCalled atomic.Bool

	// Mock suspendRemotedFunc
	suspendRemotedFunc = func() (func(), error) {
		return func() {
			resumeCalled.Store(true)
		}, nil
	}

	ctx := context.Background()

	// Use a non-existent interface - should fail on first GetInterfaceByName call
	_, err := FindRsdService(ctx, "nonexistent_test_iface_xyz")

	// Should get an error about interface not found
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "interface not found")

	// Verify resume was called even on early error return
	assert.True(t, resumeCalled.Load(), "resumeRemoted should be called even when interface is not found")
}
