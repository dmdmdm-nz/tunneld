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

func FindRsdService(ctx context.Context, interfaceName string) (RsdService, error) {
	log.WithField("interface", interfaceName).Debug("Searching for RSD service on interface")

	const maxAttempts = 30
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		entries := make(chan *zeroconf.ServiceEntry)
		resultCh := make(chan RsdService, 1)
		errCh := make(chan error, 1)

		iface, err := GetInterfaceByName(interfaceName)
		if err != nil {
			// The interface we're browsing on has gone away.
			log.WithField("interface", interfaceName).WithError(err).Debug("Failed to get interface")
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
						Error("Failed to get interface by index:", err.Error())
					continue
				}

				if resultInterface.Name != interfaceName {
					continue
				}

				resumeRemoted, err := tunnel.SuspendRemoted()
				if err != nil {
					log.WithField("interface", resultInterface.Name).
						Error("Failed to suspend remoted:", err.Error())
					continue
				}
				defer resumeRemoted()

				svc := RsdService{
					Address:       fmt.Sprintf("%s%%%s", entry.AddrIPv6[0].String(), iface.Name),
					InterfaceName: iface.Name,
				}
				svc.Udid, svc.DeviceIosVersion, err = TryGetRsdInfo(ctx, svc.Address)
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
		defer cancel()

		select {
		case svc := <-resultCh:
			browseCancel()
			return svc, nil
		case err := <-errCh:
			browseCancel()
			lastErr = err
		case <-attemptCtx.Done():
			browseCancel()
			lastErr = errors.New("no RSD service found within timeout")
		}
	}

	if lastErr == nil {
		lastErr = errors.New("no RSD service found after multiple attempts")
	}

	return RsdService{}, lastErr
}

func TryGetRsdInfo(ctx context.Context, addr string) (string, string, error) {
	s, err := tunnel.NewWithAddrPort(addr, RSD_PORT)
	if err != nil {
		return "", "", err
	}
	defer s.Close()

	h, err := s.Handshake()
	if err != nil {
		return "", "", err
	}

	return string(h.Udid), h.ProductVersion, nil
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
