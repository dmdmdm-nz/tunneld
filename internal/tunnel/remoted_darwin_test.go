//go:build darwin

package tunnel

import (
	"testing"
	"time"
)

// waitRemotedStopped is the barrier that makes a successful suspend mean "remoted has
// actually parked", not merely "SIGSTOP was sent". These tests exercise the polling
// logic with the state check stubbed, so they need no real remoted process.

func TestWaitRemotedStopped_ReturnsOnceStopped(t *testing.T) {
	origCheck, origInterval := remotedIsStopped, remotedStopPollInterval
	t.Cleanup(func() { remotedIsStopped, remotedStopPollInterval = origCheck, origInterval })

	remotedStopPollInterval = time.Millisecond
	calls := 0
	remotedIsStopped = func() bool {
		calls++
		return calls >= 3 // report stopped only on the third poll
	}

	if err := waitRemotedStopped(time.Second); err != nil {
		t.Fatalf("expected nil once remoted parks, got %v", err)
	}
	if calls < 3 {
		t.Fatalf("expected at least 3 polls before returning, got %d", calls)
	}
}

func TestWaitRemotedStopped_TimesOut(t *testing.T) {
	origCheck, origInterval := remotedIsStopped, remotedStopPollInterval
	t.Cleanup(func() { remotedIsStopped, remotedStopPollInterval = origCheck, origInterval })

	remotedStopPollInterval = time.Millisecond
	remotedIsStopped = func() bool { return false } // never parks

	if err := waitRemotedStopped(20 * time.Millisecond); err == nil {
		t.Fatal("expected a timeout error when remoted never parks, got nil")
	}
}
