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
	// indexToName maps interface index to name, populated from link events
	// This avoids calling net.InterfaceByIndex for address events
	indexToName map[int]string
}

// NewWatcher creates a Linux-specific watcher using netlink.
func NewWatcher() Watcher {
	return &linuxWatcher{
		tracked:     make(map[string]struct{}),
		indexToName: make(map[int]string),
	}
}

func (w *linuxWatcher) Start(ctx context.Context, callback func(InterfaceEvent)) error {
	// Use buffered channels to avoid dropping events during rapid changes
	linkCh := make(chan netlink.LinkUpdate, 16)
	linkDone := make(chan struct{})

	addrCh := make(chan netlink.AddrUpdate, 16)
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

	// Initialize tracked map with current interfaces (only time we scan all)
	w.reconcileAll(callback)
	log.Debug("Linux watcher initialized")

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

	// Update index-to-name mapping for NCM interfaces
	w.mu.Lock()
	if isNCMInterface(name) {
		w.indexToName[index] = name
	}
	w.mu.Unlock()

	if !isNCMInterface(name) {
		return
	}

	log.WithFields(log.Fields{
		"interface": name,
		"index":     index,
		"flags":     attrs.Flags,
	}).Trace("Received link update for NCM interface")

	// Check if interface is up using flags from the update (no net call needed)
	isUp := attrs.Flags&net.FlagUp != 0

	if !isUp {
		// Interface is down - remove from tracking without any net calls
		w.removeInterface(name, callback)
		return
	}

	// Interface is up - need to check for IPv6 addresses
	w.evaluateInterface(name, index, callback)
}

func (w *linuxWatcher) handleAddrUpdate(update netlink.AddrUpdate, callback func(InterfaceEvent)) {
	// Check if it's an IPv6 address first (no net call needed)
	ip := update.LinkAddress.IP
	if ip.To4() != nil || ip.To16() == nil {
		// Not an IPv6 address, skip
		return
	}

	// Look up interface name from our index map (no net call needed)
	w.mu.Lock()
	name, ok := w.indexToName[update.LinkIndex]
	w.mu.Unlock()

	if !ok {
		// Unknown interface - might not be NCM, or we missed the link event
		// Fall back to net.InterfaceByIndex
		iface, err := net.InterfaceByIndex(update.LinkIndex)
		if err != nil {
			return
		}
		name = iface.Name
		if !isNCMInterface(name) {
			return
		}
		// Cache it for future lookups
		w.mu.Lock()
		w.indexToName[update.LinkIndex] = name
		w.mu.Unlock()
	}

	log.WithFields(log.Fields{
		"interface": name,
		"newAddr":   update.NewAddr,
		"address":   ip,
	}).Trace("Received IPv6 address update for NCM interface")

	if update.NewAddr {
		// Address added - interface may now have IPv6
		w.mu.Lock()
		_, isTracked := w.tracked[name]
		if !isTracked {
			log.WithField("interface", name).Trace("Interface now has IPv6 address")
			w.tracked[name] = struct{}{}
			w.mu.Unlock()
			callback(InterfaceEvent{Type: InterfaceAdded, InterfaceName: name})
		} else {
			w.mu.Unlock()
		}
	} else {
		// Address removed - check if interface still has any IPv6
		w.mu.Lock()
		_, isTracked := w.tracked[name]
		w.mu.Unlock()

		if isTracked {
			// Need to check if any IPv6 addresses remain
			iface, err := net.InterfaceByIndex(update.LinkIndex)
			if err != nil || !hasIPv6Address(iface) {
				w.mu.Lock()
				delete(w.tracked, name)
				w.mu.Unlock()
				log.WithField("interface", name).Trace("Interface no longer has IPv6 address")
				callback(InterfaceEvent{Type: InterfaceRemoved, InterfaceName: name})
			}
		}
	}
}

func (w *linuxWatcher) evaluateInterface(name string, index int, callback func(InterfaceEvent)) {
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return
	}

	// Must be up
	if iface.Flags&net.FlagUp == 0 {
		w.removeInterface(name, callback)
		return
	}

	hasIPv6 := hasIPv6Address(iface)

	w.mu.Lock()
	defer w.mu.Unlock()

	_, isTracked := w.tracked[name]

	if hasIPv6 && !isTracked {
		log.WithField("interface", name).Trace("Interface now has IPv6 address")
		w.tracked[name] = struct{}{}
		callback(InterfaceEvent{Type: InterfaceAdded, InterfaceName: name})
	} else if !hasIPv6 && isTracked {
		log.WithField("interface", name).Trace("Interface no longer has IPv6 address")
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
		log.WithField("interface", name).Trace("Interface removed or down")
		callback(InterfaceEvent{Type: InterfaceRemoved, InterfaceName: name})
	}
}

func (w *linuxWatcher) reconcileAll(callback func(InterfaceEvent)) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for _, iface := range interfaces {
		// Populate index-to-name map for NCM interfaces
		if isNCMInterface(iface.Name) {
			w.indexToName[iface.Index] = iface.Name
		}

		if !isNCMInterface(iface.Name) || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if hasIPv6Address(&iface) {
			log.WithField("interface", iface.Name).Trace("Interface has IPv6 address")
			w.tracked[iface.Name] = struct{}{}
			callback(InterfaceEvent{Type: InterfaceAdded, InterfaceName: iface.Name})
		}
	}
}

// isNCMInterface returns true if the interface name matches common NCM interface patterns.
// On Linux, NCM interfaces can be named:
// - "enx..." (predictable naming with systemd)
// - "usb0", "usb1", etc. (legacy naming)
// - "en..." (some systems)
func isNCMInterface(name string) bool {
	return strings.HasPrefix(name, "en") || strings.HasPrefix(name, "usb")
}

func hasIPv6Address(iface *net.Interface) bool {
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok {
			if ipNet.IP.To4() == nil && ipNet.IP.To16() != nil {
				return true
			}
		}
	}
	return false
}
