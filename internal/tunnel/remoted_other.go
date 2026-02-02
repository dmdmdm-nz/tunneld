//go:build !darwin

package tunnel

// initRemoted is a no-op on non-macOS platforms since remoted only exists on macOS.
func initRemoted() {}

// suspendRemoted is a no-op on non-macOS platforms since remoted only exists on macOS.
func suspendRemoted() (func(), error) {
	return func() {}, nil
}

// forceResumeRemoted is a no-op on non-macOS platforms since remoted only exists on macOS.
func forceResumeRemoted() error {
	return nil
}
