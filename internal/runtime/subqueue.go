package runtime

import (
	"sync"
)

type SubQueue[T any] struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []T
	closed bool

	outCh  chan T // consumer reads from this
	paused bool   // gate dispatch until snapshot sent
}

func NewSubQueue[T any](outBuf int) *SubQueue[T] {
	sq := &SubQueue[T]{
		outCh:  make(chan T, outBuf),
		paused: true,
	}
	sq.cond = sync.NewCond(&sq.mu)
	go sq.dispatch()
	return sq
}

// Channel exposed to subscriber.
func (sq *SubQueue[T]) Chan() <-chan T { return sq.outCh }

// Enqueue appends to the in-memory queue and wakes dispatcher.
func (sq *SubQueue[T]) Enqueue(ev T) {
	sq.mu.Lock()
	if !sq.closed {
		sq.queue = append(sq.queue, ev)
		sq.cond.Signal()
	}
	sq.mu.Unlock()
}

// Pause/Resume gates dispatching (used to hold back live events during snapshot).
func (sq *SubQueue[T]) SetPaused(v bool) {
	sq.mu.Lock()
	sq.paused = v
	sq.cond.Broadcast()
	sq.mu.Unlock()
}

// Close stops the dispatcher and closes the out channel.
func (sq *SubQueue[T]) Close() {
	sq.mu.Lock()
	sq.closed = true
	sq.cond.Broadcast()
	sq.mu.Unlock()
}

func (sq *SubQueue[T]) dispatch() {
	for {
		sq.mu.Lock()
		for !sq.closed && (sq.paused || len(sq.queue) == 0) {
			sq.cond.Wait()
		}
		if sq.closed {
			sq.mu.Unlock()
			close(sq.outCh)
			return
		}
		ev := sq.queue[0]
		// pop
		copy(sq.queue, sq.queue[1:])
		sq.queue = sq.queue[:len(sq.queue)-1]
		sq.mu.Unlock()

		// Send to subscriber (blocks only on the channel buffer / reader).
		sq.outCh <- ev
	}
}
