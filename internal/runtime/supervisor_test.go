package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupervisor_AllWorkersStart(t *testing.T) {
	s := NewSupervisor()

	var started [3]atomic.Bool

	for i := 0; i < 3; i++ {
		idx := i
		s.Add("worker-"+string(rune('0'+i)), func(ctx context.Context) error {
			started[idx].Store(true)
			<-ctx.Done()
			return nil
		}, nil)
	}

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	// Give workers time to start
	time.Sleep(50 * time.Millisecond)

	// All workers should have started
	for i := 0; i < 3; i++ {
		assert.True(t, started[i].Load(), "worker %d should have started", i)
	}

	cancel()
	_ = s.Wait(ctx)
}

func TestSupervisor_ShutdownReverseOrder(t *testing.T) {
	s := NewSupervisor()

	var shutdownOrder []int
	var mu sync.Mutex

	for i := 0; i < 3; i++ {
		idx := i
		s.Add("worker-"+string(rune('0'+i)), func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		}, func() error {
			mu.Lock()
			shutdownOrder = append(shutdownOrder, idx)
			mu.Unlock()
			return nil
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	// Give workers time to start
	time.Sleep(50 * time.Millisecond)

	cancel()
	_ = s.Wait(ctx)

	// Workers should be closed in reverse order: 2, 1, 0
	assert.Equal(t, []int{2, 1, 0}, shutdownOrder)
}

func TestSupervisor_ErrorPropagation(t *testing.T) {
	s := NewSupervisor()
	expectedErr := errors.New("worker failed")

	s.Add("failing-worker", func(ctx context.Context) error {
		return expectedErr
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	// Give worker time to fail
	time.Sleep(50 * time.Millisecond)

	cancel()
	resultErr := s.Wait(ctx)

	assert.Equal(t, expectedErr, resultErr)
}

func TestSupervisor_OnlyFirstErrorReturned(t *testing.T) {
	s := NewSupervisor()

	firstErr := errors.New("first error")
	secondErr := errors.New("second error")

	var barrier sync.WaitGroup
	barrier.Add(1)

	s.Add("first-worker", func(ctx context.Context) error {
		barrier.Done()
		return firstErr
	}, nil)

	s.Add("second-worker", func(ctx context.Context) error {
		barrier.Wait() // Wait for first worker to fail
		time.Sleep(10 * time.Millisecond)
		return secondErr
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	// Give workers time to run
	time.Sleep(100 * time.Millisecond)

	cancel()
	resultErr := s.Wait(ctx)

	// Only the first error should be returned
	assert.Equal(t, firstErr, resultErr)
}

func TestSupervisor_NoError(t *testing.T) {
	s := NewSupervisor()

	s.Add("worker", func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	cancel()

	resultErr := s.Wait(ctx)
	assert.NoError(t, resultErr)
}

func TestSupervisor_ContextCancellation(t *testing.T) {
	s := NewSupervisor()

	var workerSawCancellation atomic.Bool

	s.Add("worker", func(ctx context.Context) error {
		<-ctx.Done()
		workerSawCancellation.Store(true)
		return ctx.Err()
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	cancel()

	_ = s.Wait(ctx)

	assert.True(t, workerSawCancellation.Load())
}

func TestSupervisor_NilCloseFunc(t *testing.T) {
	s := NewSupervisor()

	s.Add("worker", func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}, nil) // nil close function

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	cancel()

	// Should not panic with nil close function
	require.NotPanics(t, func() {
		_ = s.Wait(ctx)
	})
}

func TestSupervisor_CloseErrorIgnored(t *testing.T) {
	s := NewSupervisor()

	s.Add("worker", func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}, func() error {
		return errors.New("close error")
	})

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	cancel()

	// Close errors are ignored
	resultErr := s.Wait(ctx)
	assert.NoError(t, resultErr)
}

func TestSupervisor_EmptySupervisor(t *testing.T) {
	s := NewSupervisor()

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	cancel()

	// Should handle empty supervisor gracefully
	resultErr := s.Wait(ctx)
	assert.NoError(t, resultErr)
}

func TestSupervisor_WorkersRunConcurrently(t *testing.T) {
	s := NewSupervisor()

	var runningCount atomic.Int32
	var maxConcurrent atomic.Int32

	for i := 0; i < 5; i++ {
		s.Add("worker", func(ctx context.Context) error {
			count := runningCount.Add(1)
			// Update max if this is a new high
			for {
				current := maxConcurrent.Load()
				if count <= current || maxConcurrent.CompareAndSwap(current, count) {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
			runningCount.Add(-1)
			<-ctx.Done()
			return nil
		}, nil)
	}

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)
	cancel()
	_ = s.Wait(ctx)

	// All 5 workers should have been running concurrently
	assert.Equal(t, int32(5), maxConcurrent.Load())
}

func TestSupervisor_AddAfterStart(t *testing.T) {
	s := NewSupervisor()

	s.Add("initial-worker", func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	err := s.Start(ctx)
	require.NoError(t, err)

	// Add after start - this won't be started by current implementation
	// but the Add should not panic
	require.NotPanics(t, func() {
		s.Add("late-worker", func(ctx context.Context) error {
			return nil
		}, nil)
	})

	cancel()
	_ = s.Wait(ctx)
}
