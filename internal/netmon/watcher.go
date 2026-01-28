package netmon

import "context"

// Watcher monitors network interfaces for changes using platform-specific
// event mechanisms (netlink on Linux, route sockets on macOS).
type Watcher interface {
	// Start begins watching for interface changes.
	// Calls callback for each detected change.
	// Blocks until ctx is cancelled or an error occurs.
	Start(ctx context.Context, callback func(InterfaceEvent)) error
}
