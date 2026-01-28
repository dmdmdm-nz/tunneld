package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubQueue_StartsInPausedState(t *testing.T) {
	sq := NewSubQueue[int](10)
	defer sq.Close()

	// Enqueue something while paused
	sq.Enqueue(42)

	// Channel should not receive anything immediately because queue is paused
	select {
	case <-sq.Chan():
		t.Fatal("should not receive value while paused")
	case <-time.After(50 * time.Millisecond):
		// Expected: no value received
	}
}

func TestSubQueue_ResumeDeliversQueued(t *testing.T) {
	sq := NewSubQueue[int](10)
	defer sq.Close()

	// Enqueue while paused
	sq.Enqueue(1)
	sq.Enqueue(2)
	sq.Enqueue(3)

	// Resume the queue
	sq.SetPaused(false)

	// Should receive all values in order
	assert.Equal(t, 1, <-sq.Chan())
	assert.Equal(t, 2, <-sq.Chan())
	assert.Equal(t, 3, <-sq.Chan())
}

func TestSubQueue_EnqueueDequeueOrder(t *testing.T) {
	sq := NewSubQueue[int](10)
	defer sq.Close()

	sq.SetPaused(false)

	// Enqueue items
	for i := 0; i < 5; i++ {
		sq.Enqueue(i)
	}

	// Dequeue and verify order
	for i := 0; i < 5; i++ {
		select {
		case val := <-sq.Chan():
			assert.Equal(t, i, val)
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for value %d", i)
		}
	}
}

func TestSubQueue_CloseStopsDispatcher(t *testing.T) {
	sq := NewSubQueue[int](10)
	sq.SetPaused(false)

	sq.Enqueue(1)
	<-sq.Chan() // Drain

	sq.Close()

	// Channel should be closed
	select {
	case _, ok := <-sq.Chan():
		assert.False(t, ok, "channel should be closed")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestSubQueue_EnqueueAfterClose(t *testing.T) {
	sq := NewSubQueue[int](10)
	sq.SetPaused(false)
	sq.Close()

	// Enqueue after close should not panic
	require.NotPanics(t, func() {
		sq.Enqueue(42)
	})
}

func TestSubQueue_PauseAndResume(t *testing.T) {
	sq := NewSubQueue[int](10)
	defer sq.Close()

	sq.SetPaused(false)

	// Enqueue and receive
	sq.Enqueue(1)
	assert.Equal(t, 1, <-sq.Chan())

	// Pause
	sq.SetPaused(true)

	// Enqueue while paused
	sq.Enqueue(2)

	// Should not receive while paused
	select {
	case <-sq.Chan():
		t.Fatal("should not receive while paused")
	case <-time.After(50 * time.Millisecond):
		// Expected
	}

	// Resume
	sq.SetPaused(false)

	// Should receive the queued value
	select {
	case val := <-sq.Chan():
		assert.Equal(t, 2, val)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for value after resume")
	}
}

func TestSubQueue_ConcurrentEnqueue(t *testing.T) {
	sq := NewSubQueue[int](100)
	defer sq.Close()

	sq.SetPaused(false)

	numGoroutines := 10
	itemsPerGoroutine := 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Spawn goroutines to enqueue concurrently
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < itemsPerGoroutine; i++ {
				sq.Enqueue(goroutineID*100 + i)
			}
		}(g)
	}

	// Collect all values
	received := make([]int, 0, numGoroutines*itemsPerGoroutine)
	done := make(chan bool)

	go func() {
		for i := 0; i < numGoroutines*itemsPerGoroutine; i++ {
			select {
			case val := <-sq.Chan():
				received = append(received, val)
			case <-time.After(5 * time.Second):
				break
			}
		}
		done <- true
	}()

	wg.Wait() // Wait for all enqueues to complete
	<-done    // Wait for all receives to complete

	assert.Len(t, received, numGoroutines*itemsPerGoroutine)
}

func TestSubQueue_StringType(t *testing.T) {
	sq := NewSubQueue[string](10)
	defer sq.Close()

	sq.SetPaused(false)

	sq.Enqueue("hello")
	sq.Enqueue("world")

	assert.Equal(t, "hello", <-sq.Chan())
	assert.Equal(t, "world", <-sq.Chan())
}

func TestSubQueue_StructType(t *testing.T) {
	type Event struct {
		ID   int
		Name string
	}

	sq := NewSubQueue[Event](10)
	defer sq.Close()

	sq.SetPaused(false)

	sq.Enqueue(Event{ID: 1, Name: "first"})
	sq.Enqueue(Event{ID: 2, Name: "second"})

	e1 := <-sq.Chan()
	assert.Equal(t, 1, e1.ID)
	assert.Equal(t, "first", e1.Name)

	e2 := <-sq.Chan()
	assert.Equal(t, 2, e2.ID)
	assert.Equal(t, "second", e2.Name)
}

func TestSubQueue_BufferSize(t *testing.T) {
	// Test with different buffer sizes
	for _, bufSize := range []int{1, 5, 100} {
		t.Run("buffer_"+string(rune('0'+bufSize)), func(t *testing.T) {
			sq := NewSubQueue[int](bufSize)
			defer sq.Close()

			sq.SetPaused(false)

			// Enqueue more than buffer size
			for i := 0; i < bufSize*2; i++ {
				sq.Enqueue(i)
			}

			// Drain all
			for i := 0; i < bufSize*2; i++ {
				select {
				case val := <-sq.Chan():
					assert.Equal(t, i, val)
				case <-time.After(time.Second):
					t.Fatalf("timeout at index %d", i)
				}
			}
		})
	}
}

func TestSubQueue_CloseWhilePaused(t *testing.T) {
	sq := NewSubQueue[int](10)
	// Queue starts paused

	sq.Enqueue(1)
	sq.Enqueue(2)

	// Close while paused
	sq.Close()

	// Channel should be closed
	select {
	case _, ok := <-sq.Chan():
		assert.False(t, ok, "channel should be closed")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestSubQueue_MultipleCloses(t *testing.T) {
	sq := NewSubQueue[int](10)
	sq.SetPaused(false)

	sq.Close()

	// Second close should not panic
	require.NotPanics(t, func() {
		sq.Close()
	})
}
