package rsd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/dmdmdm-nz/zeroconf"
	log "github.com/sirupsen/logrus"

	tunnel "github.com/dmdmdm-nz/tunneld/internal/tunnel"
)

const RSD_PORT int = 58783

// suspendRemotedFunc is the function used to suspend remoted during discovery.
// It can be overridden in tests to verify cleanup behavior.
var suspendRemotedFunc = tunnel.SuspendRemoted

func FindRsdService(ctx context.Context, interfaceName string) (RsdService, error) {
	log.WithField("interface", interfaceName).Debug("Searching for RSD service on interface")

	// Verify interface exists before suspending remoted
	iface, err := GetInterfaceByName(interfaceName)
	if err != nil {
		log.WithField("interface", interfaceName).WithError(err).Trace("Failed to get interface")
		return RsdService{}, err
	}

	// Suspend remoted for the entire discovery process
	resumeRemoted, err := suspendRemotedFunc()
	if err != nil {
		log.WithField("interface", interfaceName).WithError(err).Warn("Failed to suspend remoted, continuing anyway")
	}
	defer func() {
		if resumeRemoted != nil {
			resumeRemoted()
		}
	}()

	const maxAttempts = 10 // approximately 30 seconds
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		entries := make(chan *zeroconf.ServiceEntry)
		resultCh := make(chan RsdService, 1)
		errCh := make(chan error, 1)

		// Re-check interface on retries (it may have gone away)
		iface, err = GetInterfaceByName(interfaceName)
		if err != nil {
			// The interface we're browsing on has gone away.
			log.WithField("interface", interfaceName).WithError(err).Trace("Failed to get interface")
			return RsdService{}, err
		}

		browseCtx, browseCancel := context.WithCancel(ctx)

		go func() {
			err := zeroconf.Browse(
				browseCtx,
				"_remoted._tcp",
				"local.",
				entries,
				zeroconf.SelectIPTraffic(zeroconf.IPv6),
				zeroconf.SelectIfaces([]net.Interface{*iface}))
			if err != nil {
				errCh <- err
				return
			}
		}()

		go func(results <-chan *zeroconf.ServiceEntry) {
			for entry := range results {
				resultInterface, err := net.InterfaceByIndex(entry.ReceivedIfIndex)
				if err != nil {
					log.WithField("index", entry.ReceivedIfIndex).
						Trace("Failed to get interface by index:", err.Error())
					continue
				}

				if resultInterface.Name != interfaceName {
					continue
				}

				svc := RsdService{
					Address:       fmt.Sprintf("%s%%%d", entry.AddrIPv6[0].String(), resultInterface.Index),
					InterfaceName: iface.Name,
				}
				svc.Udid, svc.DeviceIosVersion, svc.Services, err = TryGetRsdInfo(ctx, svc.Address)
				if err != nil {
					log.WithField("address", svc.Address).
						Trace("Failed to get UDID from address:", err.Error())
					continue
				}

				resultCh <- svc
				return
			}
		}(entries)

		attemptCtx, cancel := context.WithTimeout(ctx, 3*time.Second)

		select {
		case svc := <-resultCh:
			cancel()
			browseCancel()
			return svc, nil
		case err := <-errCh:
			log.WithField("interface", interfaceName).Error("Browse error:", err.Error())
			browseCancel()
			lastErr = err
		case <-attemptCtx.Done():
			browseCancel()
			// Check if parent context was cancelled (shutdown) vs actual timeout
			if ctx.Err() != nil {
				cancel()
				return RsdService{}, ctx.Err()
			}
			log.WithField("interface", interfaceName).Debug("Did not find an RSD service within the time out")
			lastErr = errors.New("no RSD service found within timeout")
		}
		cancel()
	}

	if lastErr == nil {
		lastErr = errors.New("no RSD service found after multiple attempts")
	}

	return RsdService{}, lastErr
}

func TryGetRsdInfo(ctx context.Context, addr string) (string, string, map[string]ServiceEntry, error) {
	s, err := tunnel.NewWithAddrPort(addr, RSD_PORT)
	if err != nil {
		return "", "", nil, err
	}
	defer s.Close()

	h, err := s.Handshake()
	if err != nil {
		return "", "", nil, err
	}

	// Convert tunnel.RsdServiceEntry to rsd.ServiceEntry
	services := make(map[string]ServiceEntry, len(h.Services))
	for name, entry := range h.Services {
		services[name] = ServiceEntry{Port: entry.Port}
	}

	return string(h.Udid), h.ProductVersion, services, nil
}

// GetInterfaceByName returns the network interface that matches the given name.
// Returns an error if the interface is not found or is not up.
func GetInterfaceByName(name string) (*net.Interface, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("interface not found: %w", err)
	}

	if iface.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("interface %s is not up", name)
	}

	return iface, nil
}
