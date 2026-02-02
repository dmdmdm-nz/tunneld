package tunnel

// InitRemoted initializes the remoted service monitoring.
// On macOS, this starts a background goroutine that monitors the remoted process.
// This should be called once at application startup.
func InitRemoted() {
	initRemoted()
}

// SuspendRemoted suspends the remoted service (macOS only).
// Returns a resume function that must be called to resume remoted.
// On non-macOS platforms, this is a no-op.
func SuspendRemoted() (func(), error) {
	return suspendRemoted()
}

// ForceResumeRemoted ensures remoted is resumed regardless of suspend count.
// This should be called during application shutdown to ensure remoted is not left suspended.
func ForceResumeRemoted() error {
	return forceResumeRemoted()
}
