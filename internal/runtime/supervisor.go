package runtime

import (
	"context"
	"sync"
)

type worker struct {
	name   string
	run    func(context.Context) error
	closeF func() error
}

type Supervisor struct {
	mu      sync.Mutex
	workers []worker
	wg      sync.WaitGroup
	errOnce sync.Once
	err     error
}

func NewSupervisor() *Supervisor {
	return &Supervisor{}
}

func (s *Supervisor) Add(name string, run func(context.Context) error, closeF func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers = append(s.workers, worker{name: name, run: run, closeF: closeF})
}

func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.workers {
		w := w
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			if err := w.run(ctx); err != nil {
				s.errOnce.Do(func() { s.err = err })
			}
		}()
	}
	return nil
}

func (s *Supervisor) Wait(ctx context.Context) error {
	<-ctx.Done() // wait for signal
	// Close in reverse order.
	for i := len(s.workers) - 1; i >= 0; i-- {
		if s.workers[i].closeF != nil {
			_ = s.workers[i].closeF()
		}
	}
	s.wg.Wait()
	return s.err
}
