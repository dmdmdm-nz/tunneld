//go:build !darwin

package tunnel

// monitorRemoted is a no-op on non-macOS platforms since remoted only exists on macOS.
func (r *RemotedService) monitorRemoted() {
	defer close(r.monitorDone)

	// Just wait for stop signal
	<-r.monitorStop
}
