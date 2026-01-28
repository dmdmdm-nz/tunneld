package netmon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dmdmdm-nz/tunneld/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockWatcher is a test double for the Watcher interface
type mockWatcher struct {
	callback func(InterfaceEvent)
	started  bool
	mu       sync.Mutex
}

func newMockWatcher() *mockWatcher {
	return &mockWatcher{}
}

func (m *mockWatcher) Start(ctx context.Context, callback func(InterfaceEvent)) error {
	m.mu.Lock()
	m.callback = callback
	m.started = true
	m.mu.Unlock()

	<-ctx.Done()
	return nil
}

func (m *mockWatcher) SendEvent(ev InterfaceEvent) {
	m.mu.Lock()
	cb := m.callback
	m.mu.Unlock()
	if cb != nil {
		cb(ev)
	}
}

// newTestService creates a Service with a mock watcher for testing
func newTestService(watcher *mockWatcher) *Service {
	s := &Service{
		watcher:           watcher,
		reconcileInterval: 1 * time.Hour, // Long interval to prevent interference
		interfaces:        make(map[string]struct{}),
		subs:              make(map[int]*runtime.SubQueue[InterfaceEvent]),
	}
	return s
}

func TestService_UpsertInterface(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)
	defer s.Close()

	// Subscribe to get events
	ch, unsub := s.Subscribe()
	defer unsub()

	// Upsert an interface
	s.UpsertInterface("en0")

	// Should receive an added event
	select {
	case ev := <-ch:
		assert.Equal(t, InterfaceAdded, ev.Type)
		assert.Equal(t, "en0", ev.InterfaceName)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for interface added event")
	}
}

func TestService_RemoveInterface(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)
	defer s.Close()

	// First add an interface
	s.UpsertInterface("en0")

	// Subscribe after adding
	ch, unsub := s.Subscribe()
	defer unsub()

	// Drain the snapshot (the added interface)
	select {
	case ev := <-ch:
		assert.Equal(t, InterfaceAdded, ev.Type)
		assert.Equal(t, "en0", ev.InterfaceName)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for snapshot")
	}

	// Now remove it
	s.RemoveInterface("en0")

	// Should receive a removed event
	select {
	case ev := <-ch:
		assert.Equal(t, InterfaceRemoved, ev.Type)
		assert.Equal(t, "en0", ev.InterfaceName)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for interface removed event")
	}
}

func TestService_RemoveInterface_NotExists(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)
	defer s.Close()

	ch, unsub := s.Subscribe()
	defer unsub()

	// Remove an interface that doesn't exist - should not panic or send event
	s.RemoveInterface("en99")

	// Should not receive any event
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected: no event
	}
}

func TestService_Subscribe_ReceivesSnapshot(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)
	defer s.Close()

	// Add some interfaces before subscribing
	s.UpsertInterface("en0")
	s.UpsertInterface("en1")
	s.UpsertInterface("en2")

	// Subscribe - should receive snapshot
	ch, unsub := s.Subscribe()
	defer unsub()

	received := make(map[string]bool)
	for i := 0; i < 3; i++ {
		select {
		case ev := <-ch:
			assert.Equal(t, InterfaceAdded, ev.Type)
			received[ev.InterfaceName] = true
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for snapshot event %d", i)
		}
	}

	assert.True(t, received["en0"])
	assert.True(t, received["en1"])
	assert.True(t, received["en2"])
}

func TestService_Subscribe_Unsubscribe(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)
	defer s.Close()

	ch, unsub := s.Subscribe()

	// Unsubscribe
	unsub()

	// Channel should be closed
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestService_MultipleSubscribers(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)
	defer s.Close()

	// Create multiple subscribers
	ch1, unsub1 := s.Subscribe()
	defer unsub1()
	ch2, unsub2 := s.Subscribe()
	defer unsub2()

	// Add an interface
	s.UpsertInterface("en0")

	// Both should receive the event
	for _, ch := range []<-chan InterfaceEvent{ch1, ch2} {
		select {
		case ev := <-ch:
			assert.Equal(t, InterfaceAdded, ev.Type)
			assert.Equal(t, "en0", ev.InterfaceName)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for event")
		}
	}
}

func TestService_HandleWatcherEvent_AddDeduplication(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)
	defer s.Close()

	ch, unsub := s.Subscribe()
	defer unsub()

	// Send the same add event twice via handleWatcherEvent
	s.handleWatcherEvent(InterfaceEvent{Type: InterfaceAdded, InterfaceName: "en0"})
	s.handleWatcherEvent(InterfaceEvent{Type: InterfaceAdded, InterfaceName: "en0"})

	// Should only receive one event (deduplicated)
	select {
	case ev := <-ch:
		assert.Equal(t, InterfaceAdded, ev.Type)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for first event")
	}

	// Should not receive second event
	select {
	case ev := <-ch:
		t.Fatalf("unexpected duplicate event: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected: no duplicate
	}
}

func TestService_HandleWatcherEvent_RemoveDeduplication(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)
	defer s.Close()

	// Add interface first
	s.handleWatcherEvent(InterfaceEvent{Type: InterfaceAdded, InterfaceName: "en0"})

	ch, unsub := s.Subscribe()
	defer unsub()

	// Drain snapshot
	<-ch

	// Remove twice
	s.handleWatcherEvent(InterfaceEvent{Type: InterfaceRemoved, InterfaceName: "en0"})
	s.handleWatcherEvent(InterfaceEvent{Type: InterfaceRemoved, InterfaceName: "en0"})

	// Should only receive one remove event
	select {
	case ev := <-ch:
		assert.Equal(t, InterfaceRemoved, ev.Type)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for remove event")
	}

	// Should not receive second event
	select {
	case ev := <-ch:
		t.Fatalf("unexpected duplicate event: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected: no duplicate
	}
}

func TestService_Close(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)

	ch, _ := s.Subscribe() // Don't call unsub, let Close handle it

	err := s.Close()
	require.NoError(t, err)

	// Channel should be closed
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed after service close")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestService_Close_Idempotent(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)

	// Close twice should not panic
	require.NotPanics(t, func() {
		_ = s.Close()
		_ = s.Close()
	})
}

func TestService_Start_ContextCancellation(t *testing.T) {
	watcher := newMockWatcher()
	s := newTestService(watcher)
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- s.Start(ctx)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Should return nil on context cancellation
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Start to return")
	}
}
