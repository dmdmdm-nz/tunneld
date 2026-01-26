package netmon

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/dmdmdm-nz/tunneld/internal/runtime"
	log "github.com/sirupsen/logrus"
)

type Service struct {
	pollInterval     time.Duration
	interfaces       map[string]struct{}
	subsMu           sync.Mutex
	subs             map[int]*runtime.SubQueue[InterfaceEvent]
	nextSubscriberID int
	mu               sync.RWMutex
	closed           bool
}

func NewService(pollInterval time.Duration) *Service {
	return &Service{
		pollInterval: pollInterval,
		interfaces:   make(map[string]struct{}),
		subs:         make(map[int]*runtime.SubQueue[InterfaceEvent]),
	}
}

func (s *Service) Subscribe() (<-chan InterfaceEvent, func()) {
	// Take a snapshot.
	s.mu.RLock()
	snapshot := make([]string, 0, len(s.interfaces))
	for interfaceName := range s.interfaces {
		snapshot = append(snapshot, interfaceName)
	}
	s.mu.RUnlock()

	// Create sub with buffer big enough for the snapshot.
	outBuf := len(snapshot) + 8
	sub := runtime.NewSubQueue[InterfaceEvent](outBuf)

	// Register subscriber in paused mode (live events will enqueue).
	s.subsMu.Lock()
	id := s.nextSubscriberID
	s.nextSubscriberID++
	s.subs[id] = sub
	s.subsMu.Unlock()

	// Emit snapshot as Adds directly to the subscriber channel.
	for _, inf := range snapshot {
		sub.OutOfBandSnapshotSend(InterfaceEvent{
			Type:          InterfaceAdded,
			InterfaceName: inf,
		})
	}

	// Transition to live: flush queued live events, then unpause.
	sub.SetPaused(false)

	// Unsubscribe closure.
	unsub := func() {
		s.subsMu.Lock()
		if q, ok := s.subs[id]; ok {
			delete(s.subs, id)
			q.Close()
		}
		s.subsMu.Unlock()
	}
	return sub.Chan(), unsub
}

func (s *Service) Start(ctx context.Context) error {
	log.Info("Starting network interface monitoring service")

	// Initial interface check
	s.checkInterfaces()

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("Stopping network interface monitoring service")
			return nil
		case <-ticker.C:
			s.checkInterfaces()
		}
	}
}

func (s *Service) Close() error {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for id, q := range s.subs {
		q.Close()
		delete(s.subs, id)
	}
	return nil
}

func (s *Service) checkInterfaces() {
	detectedInterfaces := make(map[string]struct{})

	interfaces, err := net.Interfaces()
	if err != nil {
		log.Errorf("Error getting network interfaces: %v", err)
		return
	}

	// Check all interfaces
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			// Skip down interfaces
			log.WithFields(log.Fields{
				"interface": iface.Name,
			}).Trace("Skipping down interface")
			continue
		}

		if !strings.HasPrefix(iface.Name, "en") {
			// Only check "en" interfaces
			log.WithFields(log.Fields{
				"interface": iface.Name,
			}).Trace("Skipping non-en interface")
			continue
		}

		isIPv6Only := s.checkInterfaceForIpV6(&iface)

		if isIPv6Only {
			detectedInterfaces[iface.Name] = struct{}{}
		} else {
			log.WithFields(log.Fields{
				"interface": iface.Name,
			}).Trace("Skipping non-IPv6-only interface")
		}
	}

	for interfaceName := range detectedInterfaces {
		if _, exists := s.interfaces[interfaceName]; !exists {
			s.UpsertInterface(interfaceName)
		}
	}

	for interfaceName := range s.interfaces {
		if _, exists := detectedInterfaces[interfaceName]; !exists {
			s.RemoveInterface(interfaceName)
		}
	}
}

func (s *Service) checkInterfaceForIpV6(iface *net.Interface) bool {
	addrs, err := iface.Addrs()
	if err != nil {
		log.Errorf("Error getting addresses for interface %s: %v", iface.Name, err)
		return false
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		if ipNet.IP.To16() != nil {
			return true
		}
	}

	return false
}

// UpsertInterface adds/updates an interface and publishes an Added event.
func (s *Service) UpsertInterface(interfaceName string) {
	log.WithFields(log.Fields{
		"interface": interfaceName,
	}).Info("Detected new IPv6 network interface")

	s.mu.Lock()
	s.interfaces[interfaceName] = struct{}{}
	s.mu.Unlock()

	s.broadcast(InterfaceEvent{Type: InterfaceAdded, InterfaceName: interfaceName})
}

// RemoveInterface removes an interface and publishes a Removed event.
func (s *Service) RemoveInterface(interfaceName string) {
	log.WithFields(log.Fields{
		"interface": interfaceName,
	}).Info("Detected missing IPv6 network interface")

	s.mu.Lock()
	_, found := s.interfaces[interfaceName]
	s.mu.Unlock()

	if found {
		delete(s.interfaces, interfaceName)
		s.broadcast(InterfaceEvent{Type: InterfaceRemoved, InterfaceName: interfaceName})

		log.WithFields(log.Fields{
			"interface": interfaceName,
		}).Debug("Interface event processing complete")
	}
}

func (s *Service) broadcast(ev InterfaceEvent) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, sub := range s.subs {
		sub.Enqueue(ev)
	}
}
