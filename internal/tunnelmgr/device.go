package tunnelmgr

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver"
	log "github.com/sirupsen/logrus"

	"github.com/dmdmdm-nz/tunneld/internal/rsd"
	"github.com/dmdmdm-nz/tunneld/internal/tunnel"
)

// TunnelDevice represents a single device and its lifecycle
type TunnelDevice struct {
	udid       string
	rsdInfo    rsd.RsdService
	autoCreate bool

	// State
	paired  bool
	pairing bool // true if pairing is in progress
	ready   bool
	status  tunnel.TunnelStatus
	tun     *tunnel.Tunnel

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	pm     *tunnel.PairRecordManager
	tn     *tunnel.TunnelNotifications

	mu sync.RWMutex
}

// NewTunnelDevice creates a new TunnelDevice instance
func NewTunnelDevice(
	ctx context.Context,
	rsdInfo rsd.RsdService,
	autoCreate bool,
	pm *tunnel.PairRecordManager,
	tn *tunnel.TunnelNotifications,
) *TunnelDevice {
	deviceCtx, cancel := context.WithCancel(ctx)
	return &TunnelDevice{
		udid:       rsdInfo.Udid,
		rsdInfo:    rsdInfo,
		autoCreate: autoCreate,
		pm:         pm,
		tn:         tn,
		ctx:        deviceCtx,
		cancel:     cancel,
		status:     tunnel.Disconnected,
	}
}

// Start begins the device lifecycle (pairing check, auto-tunnel if enabled)
func (d *TunnelDevice) Start() {
	// Ensure the device is running iOS 17 or greater
	if !d.isIos17OrGreater() {
		return
	}

	if d.autoCreate {
		go d.runAutoTunnel()
	} else {
		d.setReady(false)
		d.setPaired(false)
		go d.pairDevice()
	}
}

// Close cancels the device context, closes tunnel, and cleans up resources
func (d *TunnelDevice) Close() {
	d.cancel()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tun != nil {
		log.WithField("udid", d.udid).Info("Closing tunnel for device")
		d.tun.Close()
		d.tun = nil
	}
	d.status = tunnel.Disconnected
}

// CreateTunnel creates a tunnel for manual mode
func (d *TunnelDevice) CreateTunnel(ctx context.Context) (*tunnel.Tunnel, error) {
	return d.createTunnelInternal(ctx)
}

// RemoveTunnel closes an existing tunnel and returns true if a tunnel was removed
func (d *TunnelDevice) RemoveTunnel() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.tun != nil {
		log.WithField("udid", d.udid).Debug("Removing tunnel")
		d.tun.Close()
		d.tun = nil
		d.status = tunnel.Disconnected
		return true
	}
	return false
}

// GetTunnel returns the current tunnel if one exists
func (d *TunnelDevice) GetTunnel() (*tunnel.Tunnel, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.tun, d.tun != nil
}

// IsPaired returns whether the device is paired
func (d *TunnelDevice) IsPaired() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.paired
}

// IsReady returns whether the device is ready for tunnel creation
func (d *TunnelDevice) IsReady() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.ready
}

// GetStatus returns the current tunnel status
func (d *TunnelDevice) GetStatus() tunnel.TunnelStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.status
}

// GetRsdInfo returns the RSD service info for this device
func (d *TunnelDevice) GetRsdInfo() rsd.RsdService {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.rsdInfo
}

// GetUdid returns the device UDID
func (d *TunnelDevice) GetUdid() string {
	return d.udid
}

// Context returns the device context
func (d *TunnelDevice) Context() context.Context {
	return d.ctx
}

// SetStatus updates the tunnel status
func (d *TunnelDevice) SetStatus(status tunnel.TunnelStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = status
	log.WithFields(log.Fields{"udid": d.udid, "status": status.String()}).Debug("Tunnel status updated")
}

// SetPaired updates the paired status
func (d *TunnelDevice) SetPaired(paired bool) {
	d.setPaired(paired)
}

// SetReady updates the ready status
func (d *TunnelDevice) SetReady(ready bool) {
	d.setReady(ready)
}

func (d *TunnelDevice) setPaired(paired bool) {
	d.mu.Lock()
	wasPaired := d.paired
	d.paired = paired
	hasTunnel := d.tun != nil
	isPairing := d.pairing
	d.mu.Unlock()

	// If device becomes unpaired and has a tunnel, close it
	if wasPaired && !paired && hasTunnel {
		log.WithField("udid", d.udid).Info("Device became unpaired, closing tunnel")
		d.RemoveTunnel()
	}

	// In manual mode, if device becomes unpaired and not already pairing, trigger pairing
	if !d.autoCreate && wasPaired && !paired && !isPairing {
		go d.pairDevice()
	}
}

func (d *TunnelDevice) setReady(ready bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ready = ready
}

func (d *TunnelDevice) tunnelExists() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.tun != nil
}

// runAutoTunnel runs the auto-tunnel loop with retry
// Phase 1: Ensure device is paired (shows trust prompt, no timeout)
// Phase 2: Create tunnel connection (with timeout, retry on failure)
// If tunnel fails with "not paired", go back to Phase 1
func (d *TunnelDevice) runAutoTunnel() {
	for {
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		if d.tunnelExists() {
			return
		}

		// Phase 1: Ensure device is paired
		if err := d.ensurePaired(d.ctx); err != nil {
			select {
			case <-d.ctx.Done():
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}

		// Phase 2: Create tunnel
		t, err := d.createTunnelInternal(d.ctx)
		if err != nil {
			// If not paired, go back to Phase 1
			if strings.Contains(err.Error(), "device not paired") {
				log.WithField("udid", d.udid).Info("Device became unpaired, re-pairing")
				continue
			}

			select {
			case <-d.ctx.Done():
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}

		// Wait until the tunnel has exited
		<-t.TunnelContext.Done()

		log.WithFields(log.Fields{
			"interface": d.rsdInfo.InterfaceName,
			"udid":      d.udid,
			"address":   d.rsdInfo.Address,
		}).Info("Tunnel exited")

		d.RemoveTunnel()

		// Check if we're shutting down before attempting to recreate
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		log.WithFields(log.Fields{
			"interface": d.rsdInfo.InterfaceName,
			"udid":      d.udid,
			"address":   d.rsdInfo.Address,
		}).Info("Recreating tunnel")

		select {
		case <-d.ctx.Done():
			return
		case <-time.After(1 * time.Second):
			continue
		}
	}
}

// pairDevice handles pairing logic for manual mode
func (d *TunnelDevice) pairDevice() {
	// Check if we should start pairing
	d.mu.Lock()
	if d.pairing {
		d.mu.Unlock()
		log.WithField("udid", d.udid).Debug("Pairing already in progress, skipping")
		return
	}
	if d.tun != nil {
		d.mu.Unlock()
		log.WithField("udid", d.udid).Debug("Tunnel exists, skipping pairing")
		return
	}
	d.pairing = true
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.pairing = false
		d.mu.Unlock()
	}()

	if err := d.ensurePaired(d.ctx); err != nil {
		log.WithField("udid", d.udid).WithError(err).Debug("Pairing stopped")
		return
	}
	log.WithFields(log.Fields{
		"address":       d.rsdInfo.Address,
		"interface":     d.rsdInfo.InterfaceName,
		"deviceVersion": d.rsdInfo.DeviceIosVersion,
		"udid":          d.udid,
	}).Info("Device is paired and ready for tunnel creation")
}

// createTunnelInternal creates a tunnel connection
// Pairing is handled separately (by runAutoTunnel in auto mode, pairDevice in manual mode)
// Returns error if device is not paired
func (d *TunnelDevice) createTunnelInternal(ctx context.Context) (*tunnel.Tunnel, error) {
	log.WithFields(log.Fields{
		"udid":          d.udid,
		"address":       d.rsdInfo.Address,
		"interface":     d.rsdInfo.InterfaceName,
		"deviceVersion": d.rsdInfo.DeviceIosVersion,
	}).Debug("Creating tunnel to device")

	return d.connectTunnel(ctx)
}

// ensurePaired ensures the device is paired, retrying until successful
// No timeout - pairing requires user interaction (trust prompt) and can take arbitrary time
func (d *TunnelDevice) ensurePaired(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Exit if a tunnel already exists (shouldn't pair while connected)
		if d.tunnelExists() {
			log.WithField("udid", d.udid).Debug("Tunnel exists, stopping pairing")
			return nil
		}

		err := tunnel.ManualPair(ctx, d.pm, d.rsdInfo.Address, d.udid, d.getUntrustedTunnelPort(), d.tn)
		if err == nil {
			log.WithField("udid", d.udid).Debug("Device is paired")
			return nil
		}

		// Use longer delay if pairing was rejected (device may be overwhelmed)
		retryDelay := 1 * time.Second
		if errors.Is(err, tunnel.ErrPairingRejected) {
			retryDelay = 3 * time.Second
			log.WithField("udid", d.udid).WithError(err).Warn("Pairing rejected, retrying after delay")
		} else {
			log.WithFields(log.Fields{
				"udid": d.udid,
			}).WithError(err).Warn("Pairing attempt failed, retrying")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
}

// connectTunnel creates the tunnel connection with timeout and retry logic
func (d *TunnelDevice) connectTunnel(ctx context.Context) (*tunnel.Tunnel, error) {
	const attemptTimeout = 10 * time.Second

	// In auto-create mode, retry connection errors indefinitely. Otherwise, limit to 3 attempts.
	maxAttempts := 3
	if d.autoCreate {
		maxAttempts = 0 // 0 means unlimited
	}

	var t tunnel.Tunnel
	var err error
	attempts := 0

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Create a timeout context for this attempt
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)

		// autoPair=false since we already ensured pairing in phase 1
		t, err = tunnel.ManualPairAndConnectToTunnel(attemptCtx, d.pm, d.rsdInfo.Address, d.udid, d.getUntrustedTunnelPort(), false, d.tn)
		cancel()

		if err == nil {
			break // Success
		}

		// If device is not paired, return error
		// Pairing is handled separately:
		// - Auto mode: runAutoTunnel() goes back to pairing phase
		// - Manual mode: pairDevice() triggered by setPaired(false) via notification
		if strings.Contains(err.Error(), "device not paired") {
			log.WithField("udid", d.udid).Warn("Device is not paired")
			return nil, err
		}

		// Don't retry on parent context cancellation
		if ctx.Err() != nil {
			log.WithFields(log.Fields{
				"address": d.rsdInfo.Address,
				"udid":    d.udid,
			}).Debug("Tunnel connection cancelled")
			return nil, ctx.Err()
		}

		attempts++

		if maxAttempts > 0 && attempts >= maxAttempts {
			log.WithFields(log.Fields{
				"address": d.rsdInfo.Address,
				"udid":    d.udid,
			}).WithError(err).Error("Failed to connect to tunnel after all attempts")
			return nil, err
		}

		log.WithFields(log.Fields{
			"address": d.rsdInfo.Address,
			"udid":    d.udid,
			"attempt": attempts,
		}).WithError(err).Warn("Tunnel connection attempt failed, retrying")

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}

	log.WithFields(log.Fields{
		"address":       t.Address,
		"port":          t.RsdPort,
		"interface":     d.rsdInfo.InterfaceName,
		"deviceVersion": d.rsdInfo.DeviceIosVersion,
		"udid":          t.Udid,
	}).Info("Successfully created tunnel to device")

	// Check if tunnel already exists (race condition check)
	d.mu.Lock()
	if d.tun != nil {
		d.mu.Unlock()
		log.WithFields(log.Fields{
			"address": t.Address,
			"port":    t.RsdPort,
			"udid":    t.Udid,
		}).Warn("Tunnel exists, closing tunnel")
		t.Close()
		return nil, errors.New("tunnel already exists")
	}
	d.tun = &t
	d.mu.Unlock()

	return &t, nil
}

// isIos17OrGreater checks if the device is running iOS 17 or greater
func (d *TunnelDevice) isIos17OrGreater() bool {
	deviceVersion, err := semver.NewVersion(d.rsdInfo.DeviceIosVersion)
	if err != nil {
		log.
			WithFields(log.Fields{
				"udid":          d.udid,
				"deviceVersion": d.rsdInfo.DeviceIosVersion,
			}).
			WithError(err).
			Warn("Skipping tunnel creation: failed to parse iOS version")
		return false
	}

	if deviceVersion.LessThan(semver.MustParse("17.0.0")) {
		log.WithFields(log.Fields{
			"udid":          d.udid,
			"deviceVersion": d.rsdInfo.DeviceIosVersion,
		}).Debug("Skipping tunnel creation: iOS version is below 17.0.0")
		return false
	}

	return true
}

// getUntrustedTunnelPort returns the port for the untrusted tunnel service from the discovered services.
// Returns 0 if the service is not found.
func (d *TunnelDevice) getUntrustedTunnelPort() int {
	if entry, ok := d.rsdInfo.Services[tunnel.UntrustedTunnelServiceName]; ok {
		return int(entry.Port)
	}
	return 0
}
