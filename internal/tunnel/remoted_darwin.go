//go:build darwin

package tunnel

import (
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

// monitorRemoted uses kqueue to watch for process exit (restart by watchdog) and
// polls process state to detect external SIGCONT (since NOTE_CONT requires debugging
// entitlements on macOS for system processes).
func (r *RemotedService) monitorRemoted() {
	defer close(r.monitorDone)

	log.Debug("Starting remoted process monitor")
	defer log.Debug("Stopped remoted process monitor")

	for {
		pid := getRemotedPID()
		if pid == 0 {
			// remoted not running, wait and retry
			select {
			case <-r.monitorStop:
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		// Create kqueue for NOTE_EXIT detection
		kq, err := syscall.Kqueue()
		if err != nil {
			log.WithError(err).Error("Failed to create kqueue, falling back to polling only")
			r.pollOnlyMonitor()
			return
		}

		// Watch for NOTE_EXIT (process terminated/restarted)
		event := syscall.Kevent_t{
			Ident:  uint64(pid),
			Filter: syscall.EVFILT_PROC,
			Flags:  syscall.EV_ADD | syscall.EV_ENABLE | syscall.EV_CLEAR,
			Fflags: syscall.NOTE_EXIT,
		}

		_, err = syscall.Kevent(kq, []syscall.Kevent_t{event}, nil, nil)
		if err != nil {
			syscall.Close(kq)
			log.WithError(err).WithField("pid", pid).Warn("Failed to register kqueue NOTE_EXIT, falling back to polling")
			r.pollOnlyMonitor()
			return
		}

		log.WithField("pid", pid).Debug("Watching remoted process (kqueue for exit, polling for state)")

		// Watch this process until it exits or we're told to stop
		continueWatching := r.watchProcessHybrid(kq, pid)
		syscall.Close(kq)

		if !continueWatching {
			return // monitorStop was closed
		}
		// Process exited, loop back to find new PID
	}
}

// watchProcessHybrid combines kqueue NOTE_EXIT detection with polling for stopped state.
// Returns false if monitorStop was closed, true if we need to find a new PID.
func (r *RemotedService) watchProcessHybrid(kq int, pid int) bool {
	events := make([]syscall.Kevent_t, 1)
	pollInterval := 200 * time.Millisecond
	lastPollTime := time.Now()

	for {
		// Use a short timeout so we can poll state frequently
		timeout := syscall.NsecToTimespec(int64(pollInterval))
		n, err := syscall.Kevent(kq, nil, events, &timeout)

		// Check if we should stop
		select {
		case <-r.monitorStop:
			return false
		default:
		}

		if err != nil && err != syscall.EINTR {
			log.WithError(err).Error("kqueue wait error")
			return true // Try to re-watch
		}

		// Check for NOTE_EXIT event
		if n > 0 && events[0].Fflags&syscall.NOTE_EXIT != 0 {
			log.WithField("pid", pid).Warn("Remoted process exited (possibly restarted by watchdog)")

			// Wait for the new process to start and suspend it
			if r.waitAndSuspendNewRemoted(pid) {
				return true // Re-watch new PID
			}
			return false // monitorStop was closed
		}

		// Poll the process state to detect external SIGCONT
		if time.Since(lastPollTime) >= pollInterval {
			lastPollTime = time.Now()

			// Check if PID changed (shouldn't happen if we got NOTE_EXIT, but be safe)
			currentPid := getRemotedPID()
			if currentPid != pid {
				if currentPid != 0 {
					log.WithFields(log.Fields{
						"old_pid": pid,
						"new_pid": currentPid,
					}).Warn("Remoted PID changed, suspending new process")
					if err := signalRemotedSuspend(); err != nil {
						log.WithError(err).Error("Failed to suspend new remoted")
					}
				}
				return true // Re-watch
			}

			// Check if remoted is actually stopped
			if !isRemotedStopped() {
				log.WithField("pid", pid).Warn("Remoted is running (external resume detected), re-suspending")
				if err := signalRemotedSuspend(); err != nil {
					log.WithError(err).Error("Failed to re-suspend remoted")
				}
			}
		}
	}
}

// waitAndSuspendNewRemoted waits for a new remoted process to start and suspends it.
// Returns true if successful, false if monitorStop was closed.
func (r *RemotedService) waitAndSuspendNewRemoted(oldPid int) bool {
	for i := 0; i < 50; i++ { // Wait up to 5 seconds
		select {
		case <-r.monitorStop:
			return false
		case <-time.After(100 * time.Millisecond):
		}

		newPid := getRemotedPID()
		if newPid != 0 && newPid != oldPid {
			log.WithField("pid", newPid).Info("New remoted process detected, suspending")
			if err := signalRemotedSuspend(); err != nil {
				log.WithError(err).Error("Failed to suspend new remoted process")
			}
			return true
		}
	}

	log.Warn("Remoted did not restart within timeout")
	return true
}

// pollOnlyMonitor is a fallback that only uses polling (no kqueue).
func (r *RemotedService) pollOnlyMonitor() {
	log.Debug("Using poll-only monitoring")

	lastPid := 0
	for {
		select {
		case <-r.monitorStop:
			return
		case <-time.After(200 * time.Millisecond):
		}

		pid := getRemotedPID()

		// Check if remoted restarted
		if pid != 0 && lastPid != 0 && pid != lastPid {
			log.WithFields(log.Fields{
				"old_pid": lastPid,
				"new_pid": pid,
			}).Warn("Remoted process restarted, suspending")
			if err := signalRemotedSuspend(); err != nil {
				log.WithError(err).Error("Failed to suspend restarted remoted")
			}
		}
		lastPid = pid

		if pid == 0 {
			continue
		}

		if !isRemotedStopped() {
			log.WithField("pid", pid).Warn("Remoted is running (external resume detected), re-suspending")
			if err := signalRemotedSuspend(); err != nil {
				log.WithError(err).Error("Failed to re-suspend remoted")
			}
		}
	}
}
