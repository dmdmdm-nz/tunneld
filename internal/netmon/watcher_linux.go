//go:build linux

package netmon

import (
	"context"
	"net"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

type linuxWatcher struct {
	mu      sync.Mutex
	tracked map[string]struct{}
}

// NewWatcher creates a Linux-specific watcher using netlink.
func NewWatcher() Watcher {
	return &linuxWatcher{
		tracked: make(map[string]struct{}),
	}
}

func (w *linuxWatcher) Start(ctx context.Context, callback func(InterfaceEvent)) error {
	linkCh := make(chan netlink.LinkUpdate)
	linkDone := make(chan struct{})

	addrCh := make(chan netlink.AddrUpdate)
	addrDone := make(chan struct{})

	if err := netlink.LinkSubscribe(linkCh, linkDone); err != nil {
		return err
	}

	if err := netlink.AddrSubscribe(addrCh, addrDone); err != nil {
		close(linkDone)
		return err
	}

	defer close(linkDone)
	defer close(addrDone)

	for {
		select {
		case <-ctx.Done():
			return nil

		case update := <-linkCh:
			w.handleLinkUpdate(update, callback)

		case update := <-addrCh:
			w.handleAddrUpdate(update, callback)
		}
	}
}

func (w *linuxWatcher) handleLinkUpdate(update netlink.LinkUpdate, callback func(InterfaceEvent)) {
	attrs := update.Link.Attrs()
	name := attrs.Name
	index := attrs.Index

	if !strings.HasPrefix(name, "en") {
		return
	}

	// Check if interface is up
	isUp := attrs.Flags&net.FlagUp != 0

	if isUp {
		w.evaluateInterface(name, index, callback)
	} else {
		w.removeInterface(name, callback)
	}
}

func (w *linuxWatcher) handleAddrUpdate(update netlink.AddrUpdate, callback func(InterfaceEvent)) {
	iface, err := net.InterfaceByIndex(update.LinkIndex)
	if err != nil {
		return
	}

	if !strings.HasPrefix(iface.Name, "en") {
		return
	}

	w.evaluateInterface(iface.Name, iface.Index, callback)
}

func (w *linuxWatcher) evaluateInterface(name string, index int, callback func(InterfaceEvent)) {
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		log.WithError(err).WithField("interface", name).Trace("Failed to get interface by index")
		return
	}

	hasIPv6 := checkInterfaceForIPv6(iface)

	w.mu.Lock()
	defer w.mu.Unlock()

	_, isTracked := w.tracked[name]

	if hasIPv6 && !isTracked {
		w.tracked[name] = struct{}{}
		callback(InterfaceEvent{Type: InterfaceAdded, InterfaceName: name})
	} else if !hasIPv6 && isTracked {
		delete(w.tracked, name)
		callback(InterfaceEvent{Type: InterfaceRemoved, InterfaceName: name})
	}
}

func (w *linuxWatcher) removeInterface(name string, callback func(InterfaceEvent)) {
	w.mu.Lock()
	_, isTracked := w.tracked[name]
	delete(w.tracked, name)
	w.mu.Unlock()

	if isTracked {
		callback(InterfaceEvent{Type: InterfaceRemoved, InterfaceName: name})
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
