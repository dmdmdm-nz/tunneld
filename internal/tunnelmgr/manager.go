package tunnelmgr

import (
	"context"
	"errors"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/dmdmdm-nz/tunneld/internal/rsd"
	"github.com/dmdmdm-nz/tunneld/internal/tunnel"
)

// Manager manages the collection of devices
type Manager struct {
	autoCreateTunnels bool

	rsdCh    <-chan rsd.RsdServiceEvent
	rsdUnsub func()

	devicesMu sync.RWMutex
	devices   map[string]*TunnelDevice // udid -> device

	pm     *tunnel.PairRecordManager
	tn     *tunnel.TunnelNotifications
	closed bool
}

// NewManager creates a new tunnel manager
func NewManager(autoCreateTunnels bool) *Manager {
	pm, err := tunnel.NewPairRecordManager(".")
	if err != nil {
		log.WithError(err).Fatal("Failed to create PairRecordManager")
	}

	tn := tunnel.NewTunnelNotifications()

	return &Manager{
		autoCreateTunnels: autoCreateTunnels,
		devices:           make(map[string]*TunnelDevice),
		pm:                &pm,
		tn:                tn,
	}
}

// AttachRSD subscribes to RSD events
func (m *Manager) AttachRSD(ch <-chan rsd.RsdServiceEvent, unsub func()) {
	m.rsdCh = ch
	m.rsdUnsub = unsub
}

// Start begins the main event loop
func (m *Manager) Start(ctx context.Context) error {
	log.Info("Starting TunnelManager service")
	defer log.Info("Stopping TunnelManager service")

	if m.rsdCh == nil {
		log.Error("AttachRSD was not called before Start")
		<-ctx.Done()
		return nil
	}

	// Subscribe to tunnel notifications and listen for new events
	tunnelEvents, unsub := m.tn.Subscribe()
	defer unsub()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-tunnelEvents:
				if !ok {
					return
				}

				m.handleTunnelEvent(ev)
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-m.rsdCh:
			if !ok {
				return nil
			}
			switch ev.Type {
			case rsd.RsdServiceAdded:
				m.handleRsdAdded(ctx, ev.Info)
			case rsd.RsdServiceRemoved:
				m.handleRsdRemoved(ev.Info.Udid)
			}
		}
	}
}

// Close closes all devices and unsubscribes
func (m *Manager) Close() error {
	if m.rsdUnsub != nil {
		m.rsdUnsub()
	}

	m.devicesMu.Lock()
	defer m.devicesMu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true

	// Close all devices
	for udid, device := range m.devices {
		log.WithField("udid", udid).Debug("Closing device")
		device.Close()
		delete(m.devices, udid)
	}

	return nil
}

// handleRsdAdded creates a new TunnelDevice and starts it
func (m *Manager) handleRsdAdded(ctx context.Context, info rsd.RsdService) {
	m.devicesMu.Lock()
	defer m.devicesMu.Unlock()

	if _, exists := m.devices[info.Udid]; exists {
		log.WithField("udid", info.Udid).Debug("Device already exists, skipping")
		return
	}

	device := NewTunnelDevice(ctx, info, m.autoCreateTunnels, m.pm, m.tn)
	m.devices[info.Udid] = device
	device.Start()
}

// handleRsdRemoved closes and removes a device
func (m *Manager) handleRsdRemoved(udid string) {
	m.devicesMu.Lock()
	device, exists := m.devices[udid]
	if exists {
		delete(m.devices, udid)
	}
	m.devicesMu.Unlock()

	if exists {
		device.Close()
	}
}

// handleTunnelEvent handles tunnel notification events
func (m *Manager) handleTunnelEvent(ev tunnel.TunnelEvent) {
	m.devicesMu.RLock()
	device, exists := m.devices[ev.Udid]
	m.devicesMu.RUnlock()

	if !exists {
		return
	}

	switch ev.Type {
	case tunnel.DeviceNotPaired:
		device.SetPaired(false)
		device.SetReady(true)
	case tunnel.DevicePaired:
		device.SetPaired(true)
		device.SetReady(true)
	case tunnel.TunnelProgress:
		device.SetStatus(ev.Status)
	}
}

// GetDevice returns a device by UDID
func (m *Manager) GetDevice(udid string) (*TunnelDevice, bool) {
	m.devicesMu.RLock()
	defer m.devicesMu.RUnlock()
	device, ok := m.devices[udid]
	return device, ok
}

// GetAllDevices returns all devices
func (m *Manager) GetAllDevices() []*TunnelDevice {
	m.devicesMu.RLock()
	defer m.devicesMu.RUnlock()

	devices := make([]*TunnelDevice, 0, len(m.devices))
	for _, device := range m.devices {
		devices = append(devices, device)
	}
	return devices
}

// GetTunnel returns a tunnel by UDID
func (m *Manager) GetTunnel(udid string) (*tunnel.Tunnel, bool) {
	m.devicesMu.RLock()
	device, ok := m.devices[udid]
	m.devicesMu.RUnlock()

	if !ok {
		return nil, false
	}
	return device.GetTunnel()
}

// GetAllTunnels returns all active tunnels
func (m *Manager) GetAllTunnels() []*tunnel.Tunnel {
	m.devicesMu.RLock()
	defer m.devicesMu.RUnlock()

	tunnels := make([]*tunnel.Tunnel, 0)
	for _, device := range m.devices {
		if t, ok := device.GetTunnel(); ok {
			tunnels = append(tunnels, t)
		}
	}
	return tunnels
}

// IsAutoCreateEnabled returns whether auto-create is enabled
func (m *Manager) IsAutoCreateEnabled() bool {
	return m.autoCreateTunnels
}

// CreateTunnel creates a tunnel for a device (manual mode)
func (m *Manager) CreateTunnel(ctx context.Context, udid string) (*tunnel.Tunnel, error) {
	m.devicesMu.RLock()
	device, ok := m.devices[udid]
	m.devicesMu.RUnlock()

	if !ok {
		return nil, errors.New("device not found")
	}

	return device.CreateTunnel(ctx)
}

// RemoveTunnel removes a tunnel for a device
func (m *Manager) RemoveTunnel(udid string) {
	m.devicesMu.RLock()
	device, ok := m.devices[udid]
	m.devicesMu.RUnlock()

	if ok {
		device.RemoveTunnel()
	}
}

// DeviceExists checks if a device exists
func (m *Manager) DeviceExists(udid string) bool {
	m.devicesMu.RLock()
	defer m.devicesMu.RUnlock()
	_, ok := m.devices[udid]
	return ok
}

// IsDeviceReady checks if a device is ready
func (m *Manager) IsDeviceReady(udid string) bool {
	m.devicesMu.RLock()
	device, ok := m.devices[udid]
	m.devicesMu.RUnlock()

	if !ok {
		return false
	}
	return device.IsReady()
}

// IsDevicePaired checks if a device is paired
func (m *Manager) IsDevicePaired(udid string) bool {
	m.devicesMu.RLock()
	device, ok := m.devices[udid]
	m.devicesMu.RUnlock()

	if !ok {
		return false
	}
	return device.IsPaired()
}
