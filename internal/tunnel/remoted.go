package tunnel

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

type RemotedService struct {
	mu           sync.Mutex
	cond         *sync.Cond
	suspendCount int
	isSuspended  bool
	suspending   bool // true while suspend exec.Command is in progress
	resuming     bool // true while resume exec.Command is in progress

	// Monitor goroutine management
	monitorStop chan struct{}
	monitorDone chan struct{}
}

var (
	instance *RemotedService
	once     sync.Once
)

func SuspendRemoted() (func(), error) {
	once.Do(func() {
		instance = &RemotedService{}
		instance.cond = sync.NewCond(&instance.mu)
	})

	return instance.suspendRemoted()
}

// ForceResumeRemoted ensures remoted is resumed regardless of suspend count.
// This should be called during application shutdown to ensure remoted is not left suspended.
func ForceResumeRemoted() error {
	if instance == nil {
		return nil
	}
	return instance.forceResume()
}

func (r *RemotedService) forceResume() error {
	r.mu.Lock()

	// Wait if someone else is currently suspending or resuming
	for r.suspending || r.resuming {
		r.cond.Wait()
	}

	if !r.isSuspended {
		r.mu.Unlock()
		return nil
	}

	// Stop the monitor before resuming
	r.stopMonitor()

	// We need to resume - mark that we're resuming and release the mutex
	r.resuming = true
	r.mu.Unlock()

	// Perform the actual resume action without holding the mutex
	err := signalRemotedResume()

	r.mu.Lock()
	r.resuming = false
	if err != nil {
		r.cond.Broadcast()
		r.mu.Unlock()
		return err
	}

	r.isSuspended = false
	r.suspendCount = 0
	r.cond.Broadcast()
	r.mu.Unlock()

	return nil
}

// suspendRemoted suspends the remoted service by sending a SIGSTOP signal.
// It returns a function that, when called, will resume the remoted service.
func (r *RemotedService) suspendRemoted() (func(), error) {
	if runtime.GOOS != "darwin" {
		return func() {}, nil // Only suspend on macOS
	}

	r.mu.Lock()

	// Wait if someone else is currently suspending or resuming
	for r.suspending || r.resuming {
		r.cond.Wait()
	}

	// If already suspended, just increment count and return
	if r.isSuspended {
		r.suspendCount++
		log.Trace("Remoted service is already suspended; current count:", r.suspendCount)
		r.mu.Unlock()
		return r.createResumeFunc(), nil
	}

	// We need to suspend - mark that we're suspending and release the mutex
	r.suspending = true
	r.mu.Unlock()

	// Perform the actual suspend action without holding the mutex
	err := signalRemotedSuspend()

	r.mu.Lock()
	r.suspending = false
	if err != nil {
		r.cond.Broadcast() // Wake up any waiters so they can try
		r.mu.Unlock()
		return nil, fmt.Errorf("failed to suspend remoted: %v", err)
	}

	r.isSuspended = true
	r.suspendCount++

	// Start the monitor on first suspend
	if r.suspendCount == 1 {
		r.startMonitor()
	}

	r.cond.Broadcast() // Wake up any waiters
	r.mu.Unlock()

	return r.createResumeFunc(), nil
}

// createResumeFunc returns a function that decrements the suspend count
// and resumes remoted when the count reaches zero.
func (r *RemotedService) createResumeFunc() func() {
	return func() {
		r.mu.Lock()

		// Wait if someone else is currently suspending or resuming
		for r.suspending || r.resuming {
			r.cond.Wait()
		}

		if r.suspendCount > 0 {
			r.suspendCount--
			log.Trace("Decremented suspend count; current count:", r.suspendCount)
		}

		if r.suspendCount == 0 && r.isSuspended {
			// Stop the monitor before resuming
			r.stopMonitor()

			// We need to resume - mark that we're resuming and release the mutex
			r.resuming = true
			r.mu.Unlock()

			// Perform the actual resume action without holding the mutex
			err := signalRemotedResume()

			r.mu.Lock()
			r.resuming = false
			if err == nil {
				r.isSuspended = false
			}
			r.cond.Broadcast() // Wake up any waiters
			r.mu.Unlock()
			return
		}

		r.mu.Unlock()
	}
}

// signalRemotedSuspend sends a SIGSTOP signal to all processes named "remoted"
// This suspends the process until a SIGCONT signal is received.
func signalRemotedSuspend() error {
	// Execute "killall -STOP remoted"
	cmd := exec.Command("sudo", "killall", "-STOP", "remoted")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Errorf("failed to suspend remoted: %v, output: %s", err, out)
		return fmt.Errorf("failed to suspend remoted: %v, output: %s", err, out)
	}

	log.Debug("Suspended remoted service")
	return nil
}

// signalRemotedResume sends a SIGCONT signal to all processes named "remoted"
// This resumes the process that was suspended using SIGSTOP.
func signalRemotedResume() error {
	// Execute "killall -CONT remoted"
	cmd := exec.Command("sudo", "killall", "-CONT", "remoted")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Errorf("failed to resume remoted: %v, output: %s", err, out)
		return fmt.Errorf("failed to resume remoted: %v, output: %s", err, out)
	}

	log.Debug("Resumed remoted service")
	return nil
}

// getRemotedPID returns the PID of the remoted process, or 0 if not found.
func getRemotedPID() int {
	cmd := exec.Command("pgrep", "-x", "remoted")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	// pgrep may return multiple PIDs, take the first one
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		return 0
	}
	return pid
}

// isRemotedStopped checks if the remoted process is actually in stopped state.
func isRemotedStopped() bool {
	// Use ps to check process state - 'T' means stopped
	cmd := exec.Command("sh", "-c", "ps -o state= -p $(pgrep -x remoted 2>/dev/null) 2>/dev/null")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(out))
	// State 'T' means stopped (SIGSTOP), 'T+' means stopped in foreground
	return strings.HasPrefix(state, "T")
}

// startMonitor starts the kqueue-based monitor that watches for external
// resume (SIGCONT) or restart of the remoted process.
func (r *RemotedService) startMonitor() {
	r.monitorStop = make(chan struct{})
	r.monitorDone = make(chan struct{})

	go r.monitorRemoted()
}

// stopMonitor stops the monitor goroutine.
func (r *RemotedService) stopMonitor() {
	if r.monitorStop != nil {
		close(r.monitorStop)
		<-r.monitorDone // Wait for monitor to finish
		r.monitorStop = nil
		r.monitorDone = nil
	}
}
