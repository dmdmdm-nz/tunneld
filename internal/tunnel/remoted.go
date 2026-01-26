package tunnel

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"

	log "github.com/sirupsen/logrus"
)

type RemotedService struct {
	mu           sync.Mutex
	suspendCount int
	isSuspended  bool
}

var (
	instance *RemotedService
	once     sync.Once
)

func SuspendRemoted() (func(), error) {
	once.Do(func() {
		instance = &RemotedService{}
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
	defer r.mu.Unlock()

	if !r.isSuspended {
		return nil
	}

	if err := signalRemotedResume(); err != nil {
		return err
	}

	r.isSuspended = false
	r.suspendCount = 0

	return nil
}

// suspendRemoted suspends the remoted service by sending a SIGSTOP signal.
// It returns a function that, when called, will resume the remoted service.
func (r *RemotedService) suspendRemoted() (func(), error) {
	if runtime.GOOS != "darwin" {
		return func() {}, nil // Only suspend on macOS
	}

	r.mu.Lock()
	if r.suspendCount == 0 && !r.isSuspended {
		// Perform the actual suspend action here
		if err := signalRemotedSuspend(); err != nil {
			return nil, fmt.Errorf("failed to suspend remoted: %v", err)
		}

		r.isSuspended = true
	} else {
		log.Trace("Remoted service is already suspended; current count:", r.suspendCount+1)
	}
	r.suspendCount++
	r.mu.Unlock()

	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()

		if r.suspendCount > 0 {
			log.Trace("Not resuming remoted service; current count:", r.suspendCount-1)
			r.suspendCount--
		}

		if r.suspendCount == 0 && r.isSuspended {
			if err := signalRemotedResume(); err != nil {
				return
			}

			r.isSuspended = false
		}
	}, nil
}

// SuspendRemoted sends a SIGSTOP signal to all processes named "remoted"
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

// ResumeRemoted sends a SIGCONT signal to all processes named "remoted"
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
