//go:build darwin

package netmon

import (
	"context"
	"net"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type darwinWatcher struct {
	mu      sync.Mutex
	tracked map[string]struct{}
}

// NewWatcher creates a macOS-specific watcher using AF_ROUTE sockets.
func NewWatcher() Watcher {
	return &darwinWatcher{
		tracked: make(map[string]struct{}),
	}
}

func (w *darwinWatcher) Start(ctx context.Context, callback func(InterfaceEvent)) error {
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return err
	}

	// Close socket when context is cancelled
	go func() {
		<-ctx.Done()
		unix.Close(fd)
	}()

	// Initialize tracked map with current interfaces
	w.reconcileAll(callback)
	log.Debug("Darwin watcher initialized")

	buf := make([]byte, 4096)

	for {
		n, err := unix.Read(fd, buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.WithError(err).Warn("Error reading from route socket")
				continue
			}
		}

		if n == 0 {
			continue
		}

		// Any message from the routing socket indicates a network change.
		// Rather than trying to parse (ParseRIB has known Darwin bugs:
		// golang/go#44740, #70528, #71064), just reconcile the full interface list.
		log.Trace("Received route socket message, reconciling interfaces")
		w.reconcileAll(callback)
	}
}

func (w *darwinWatcher) reconcileAll(callback func(InterfaceEvent)) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return
	}

	current := make(map[string]struct{})
	for _, iface := range interfaces {
		if !strings.HasPrefix(iface.Name, "en") || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if checkInterfaceForIPv6(&iface) {
			current[iface.Name] = struct{}{}
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Find new interfaces
	for name := range current {
		if _, exists := w.tracked[name]; !exists {
			log.WithField("interface", name).Debug("Interface now has IPv6 address")
			w.tracked[name] = struct{}{}
			callback(InterfaceEvent{Type: InterfaceAdded, InterfaceName: name})
		}
	}

	// Find removed interfaces
	for name := range w.tracked {
		if _, exists := current[name]; !exists {
			log.WithField("interface", name).Debug("Interface no longer has IPv6 address")
			delete(w.tracked, name)
			callback(InterfaceEvent{Type: InterfaceRemoved, InterfaceName: name})
		}
	}
}

func checkInterfaceForIPv6(iface *net.Interface) bool {
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			// To4() returns nil for IPv6 addresses, non-nil for IPv4
			if ipNet.IP.To4() == nil && ipNet.IP.To16() != nil {
				return true
			}
		}
	}
	return false
}
