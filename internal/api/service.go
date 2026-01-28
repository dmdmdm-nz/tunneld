package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver"
	log "github.com/sirupsen/logrus"

	"github.com/dmdmdm-nz/tunneld/internal/rsd"
	"github.com/dmdmdm-nz/tunneld/internal/tunnel"
)

// Service represents the HTTP server for the API
type Service struct {
	address           string
	port              int
	autoCreateTunnels bool

	rsdCh       <-chan rsd.RsdServiceEvent
	rsdUnsub    func()
	rsdMapMutex sync.RWMutex
	rsdMap      map[string]rsd.RsdService

	tunnelsMutex sync.RWMutex
	tunnels      map[string]*tunnel.Tunnel

	devicesReadyMutex sync.Mutex
	devicesReady      map[string]bool

	pairedStatusMutex sync.Mutex
	pairedStatus      map[string]bool

	tunnelsStatusMutex sync.Mutex
	tunnelsStatus      map[string]tunnel.TunnelStatus

	pm     *tunnel.PairRecordManager
	tn     *tunnel.TunnelNotifications
	closed bool
}

func NewService(host string, port int, autoCreateTunnels bool) *Service {
	pm, err := tunnel.NewPairRecordManager(".")
	if err != nil {
		log.WithError(err).Fatal("Failed to create PairRecordManager")
	}

	tn := tunnel.NewTunnelNotifications()

	return &Service{
		address:           host,
		port:              port,
		autoCreateTunnels: autoCreateTunnels,
		pm:                &pm,
		tn:                tn,
		tunnels:           make(map[string]*tunnel.Tunnel),
		tunnelsStatus:     make(map[string]tunnel.TunnelStatus),
		pairedStatus:      make(map[string]bool),
		devicesReady:      make(map[string]bool),
		rsdMap:            make(map[string]rsd.RsdService),
	}
}

func (s *Service) AttachRSD(ch <-chan rsd.RsdServiceEvent, unsub func()) {
	s.rsdCh = ch
	s.rsdUnsub = unsub
}

// Start initializes and starts the HTTP server
func (s *Service) Start(ctx context.Context) error {
	go func() {
		if err := s.startApiService(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).Error("Failed to start the API service")
		}
	}()

	log.Infof("Starting TunnelD API service at %s:%d\n", s.address, s.port)
	defer log.Info("Stopping TunnelD API service")

	if s.rsdCh == nil {
		log.Error("AttachRSD was not called before Start")
		<-ctx.Done()
		return nil
	}

	// Subscribe to tunnel notifications and listen for new events
	tunnelEvents, unsub := s.tn.Subscribe()
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

				switch ev.Type {
				case tunnel.DeviceNotPaired:
					s.setPaired(ev.Udid, false)
					s.setDeviceReady(ev.Udid, true)
				case tunnel.DevicePaired:
					s.setPaired(ev.Udid, true)
					s.setDeviceReady(ev.Udid, true)
				case tunnel.TunnelProgress:
					s.setTunnelStatus(ev.Udid, ev.Status)
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-s.rsdCh:
			if !ok {
				return nil
			}
			switch ev.Type {
			case rsd.RsdServiceAdded:
				s.setRsd(ev.Info)
				if s.autoCreateTunnels {
					go func() {
						s.runAutoTunnel(ctx, ev)
					}()
				} else {
					s.setDeviceReady(ev.Info.Udid, false)
					s.setPaired(ev.Info.Udid, false)
				}
			case rsd.RsdServiceRemoved:
				s.removeRsd(ev.Info.Udid)
				s.removeTunnel(ev.Info)
			}
		}
	}
}

func (s *Service) Close() error {
	if s.rsdUnsub != nil {
		s.rsdUnsub()
	}

	s.tunnelsMutex.Lock()
	defer s.tunnelsMutex.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	// Close all the tunnels
	for udid, t := range s.tunnels {
		log.WithField("udid", udid).Info("Closing all tunnels")
		t.Close()
		delete(s.tunnels, udid)
		s.setTunnelStatus(udid, tunnel.Disconnected)
	}

	return nil
}

func (s *Service) startApiService(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			ctx.Done()
			w.WriteHeader(http.StatusOK)
			time.Sleep(1 * time.Second)
			os.Exit(0)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/tunnel/", func(w http.ResponseWriter, r *http.Request) {
		udid := strings.TrimPrefix(r.URL.Path, "/tunnel/")
		if len(udid) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodGet:
			s.tunnelsMutex.RLock()
			tunnel, ok := s.tunnels[string(udid)]
			s.tunnelsMutex.RUnlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Add("Content-Type", "application/json")
			enc := json.NewEncoder(w)
			err := enc.Encode(tunnel)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to encode tunnel info: %v", err), http.StatusInternalServerError)
				return
			}
		case http.MethodDelete:
			s.removeTunnel(rsd.RsdService{Udid: string(udid)})
			w.WriteHeader(http.StatusOK)
			return
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/services/", func(w http.ResponseWriter, r *http.Request) {
		udid := strings.TrimPrefix(r.URL.Path, "/services/")
		if len(udid) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodGet:
			s.tunnelsMutex.RLock()
			tunnel, ok := s.tunnels[string(udid)]
			s.tunnelsMutex.RUnlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Add("Content-Type", "application/json")
			enc := json.NewEncoder(w)
			err := enc.Encode(tunnel.Services)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to encode tunnel info: %v", err), http.StatusInternalServerError)
				return
			}
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/tunnels", func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			writer.Header().Add("Content-Type", "application/json")
			enc := json.NewEncoder(writer)

			s.tunnelsMutex.RLock()
			defer s.tunnelsMutex.RUnlock()

			tunnels := make([]tunnel.Tunnel, 0, len(s.tunnels))
			for _, t := range s.tunnels {
				tunnels = append(tunnels, *t)
			}
			err := enc.Encode(tunnels)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			}
		default:
			http.Error(writer, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/ws/create-tunnel", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			udid := r.URL.Query().Get("udid")
			if udid == "" {
				http.Error(w, "missing udid", http.StatusBadRequest)
				return
			}

			if s.autoCreateTunnels {
				http.Error(w, "Cannot manually create tunnels when autoCreateTunnels is enabled.", http.StatusMethodNotAllowed)
				return
			}

			if !s.rsdExists(udid) {
				http.Error(w, "No RSD service found for the given UDID", http.StatusNotFound)
				return
			}

			if !s.isDeviceReady(udid) {
				http.Error(w, "Device is not ready", http.StatusNotFound)
				return
			}

			if !s.isPaired(udid) {
				http.Error(w, "Device has not been paired", http.StatusConflict)
				return
			}

			CreateWebSocketTunnel(s, string(udid), w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/ws/pause-remoted", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			PauseRemoteD(s, w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.createTunnelStatusResponse(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.createPrometheusMetricsResponse(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	addr := fmt.Sprintf("%s:%d", s.address, s.port)
	return http.ListenAndServe(addr, mux)
}

func (s *Service) runAutoTunnel(ctx context.Context, ev rsd.RsdServiceEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if s.tunnelExists(ev.Info.Udid) {
			// Tunnel already exists.
			return
		}

		if !s.rsdExists(ev.Info.Udid) {
			// RSD service no longer exists.
			return
		}

		// Ensure the device is running iOS 17 or greater
		if !s.isIos17OrGreater(ev.Info) {
			return
		}

		tunnel, err := s.createTunnel(ctx, ev.Info)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}

		// Wait until the tunnel has exited
		<-tunnel.TunnelContext.Done()

		log.WithFields(log.Fields{
			"interface": ev.Info.InterfaceName,
			"udid":      ev.Info.Udid,
			"addr":      ev.Info.Address,
		}).Info("Tunnel exited")

		s.removeTunnel(ev.Info)

		if s.autoCreateTunnels {
			if s.rsdExists(ev.Info.Udid) {
				// Check if we're shutting down before attempting to recreate
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Try to recreate the tunnel
				log.WithFields(log.Fields{
					"interface": ev.Info.InterfaceName,
					"udid":      ev.Info.Udid,
					"addr":      ev.Info.Address,
				}).Info("Recreating tunnel")

				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
					continue
				}
			}
		}

		log.Debug("Interface is gone, not recreating tunnel for ", ev.Info.Udid)
		return
	}
}

func (s *Service) pairTunnel(udid string) {
	// Get the RSD service info
	rsd, ok := s.getRsd(udid)
	if !ok {
		log.WithField("udid", udid).Warn("No RSD service found for device, cannot pair tunnel")
		return
	}

	// Ensure the device is running iOS 17 or greater
	if !s.isIos17OrGreater(rsd) {
		return
	}

	ctx := context.Background()

	for {
		// Check if device is still connected before each attempt
		if !s.rsdExists(udid) {
			log.WithField("udid", udid).Debug("Device disconnected, stopping pairing attempts")
			return
		}

		err := tunnel.ManualPair(ctx, s.pm, rsd.Address, rsd.Udid, s.tn)
		if err == nil {
			break
		}

		if strings.Contains(err.Error(), "new pairing created, re-attempting connection") {
			break
		}

		log.WithFields(log.Fields{
			"udid": rsd.Udid,
		}).Info("Re-attempting tunnel pairing")

		// Check again before sleeping to avoid unnecessary delay
		if !s.rsdExists(udid) {
			log.WithField("udid", udid).Debug("Device disconnected, stopping pairing attempts")
			return
		}

		time.Sleep(1 * time.Second)
	}

	log.WithFields(log.Fields{
		"udid": rsd.Udid,
	}).Info("Device is paired and ready for tunnel creation")
}

func (s *Service) isIos17OrGreater(rsd rsd.RsdService) bool {
	deviceVersion, err := semver.NewVersion(rsd.DeviceIosVersion)
	if err != nil {
		log.
			WithFields(log.Fields{
				"udid":          rsd.Udid,
				"deviceVersion": rsd.DeviceIosVersion,
			}).
			WithError(err).
			Warn("Skipping tunnel creation: failed to parse iOS version")
		return false
	}

	if deviceVersion.LessThan(semver.MustParse("17.0.0")) {
		log.WithFields(log.Fields{
			"udid":          rsd.Udid,
			"deviceVersion": rsd.DeviceIosVersion,
		}).Debug("Skipping tunnel creation: iOS version is below 17.0.0")
		return false
	}

	return true
}

func (s *Service) createTunnel(ctx context.Context, info rsd.RsdService) (*tunnel.Tunnel, error) {
	log.WithFields(log.Fields{
		"udid":          info.Udid,
		"address":       info.Address,
		"interface":     info.InterfaceName,
		"deviceVersion": info.DeviceIosVersion,
	}).Info("Creating tunnel to device")

	var t tunnel.Tunnel
	var err error
	for {
		t, err = tunnel.ManualPairAndConnectToTunnel(ctx, s.pm, info.Address, info.Udid, s.autoCreateTunnels, s.tn)
		if err != nil {
			if strings.Contains(err.Error(), "new pairing created, re-attempting connection") {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(1 * time.Second):
					log.WithFields(log.Fields{
						"udid": info.Udid,
					}).Info("Re-attempting tunnel creation after new pairing")
					continue
				}
			}

			log.WithFields(log.Fields{
				"address": info.Address,
				"udid":    info.Udid,
			}).WithError(err).Error("Failed to connect to tunnel")
			return nil, err
		}
		break
	}

	log.WithFields(log.Fields{
		"address": t.Address,
		"port":    t.RsdPort,
		"udid":    t.Udid,
	}).Info("Successfully connected to tunnel")

	s.tunnelsMutex.Lock()
	defer s.tunnelsMutex.Unlock()
	if _, ok := s.tunnels[info.Udid]; ok {
		// Tunnel already exists.
		log.WithFields(log.Fields{
			"address": t.Address,
			"port":    t.RsdPort,
			"udid":    t.Udid,
		}).Warn("Tunnel exists, closing tunnel")
		t.Close()
		return nil, fmt.Errorf("tunnel already exists")
	}
	s.tunnels[info.Udid] = &t

	return &t, nil
}

func (s *Service) removeTunnel(info rsd.RsdService) {
	s.tunnelsMutex.Lock()
	defer s.tunnelsMutex.Unlock()
	if t, ok := s.tunnels[info.Udid]; ok {
		log.WithField("udid", info.Udid).Info("Removing tunnel")
		delete(s.tunnels, info.Udid)
		s.setTunnelStatus(info.Udid, tunnel.Disconnected)
		t.Close()
	}
}

func (s *Service) tunnelExists(udid string) bool {
	s.tunnelsMutex.RLock()
	defer s.tunnelsMutex.RUnlock()
	_, exists := s.tunnels[udid]
	return exists
}

func (s *Service) isDeviceReady(udid string) bool {
	s.devicesReadyMutex.Lock()
	defer s.devicesReadyMutex.Unlock()
	ready, ok := s.devicesReady[udid]
	return ready && ok
}

func (s *Service) setDeviceReady(udid string, ready bool) {
	s.devicesReadyMutex.Lock()
	defer s.devicesReadyMutex.Unlock()
	s.devicesReady[udid] = ready
}

func (s *Service) isPaired(udid string) bool {
	s.pairedStatusMutex.Lock()
	defer s.pairedStatusMutex.Unlock()
	paired, ok := s.pairedStatus[udid]
	return paired && ok
}

func (s *Service) setPaired(udid string, paired bool) {
	s.pairedStatusMutex.Lock()
	defer s.pairedStatusMutex.Unlock()

	needsPairing := false
	if wasPaired, ok := s.pairedStatus[udid]; !ok || (wasPaired && !paired) {
		needsPairing = true
	}

	s.pairedStatus[udid] = paired

	if needsPairing {
		go func() {
			s.pairTunnel(udid)
		}()
	}
}

func (s *Service) getTunnelStatus(udid string) tunnel.TunnelStatus {
	s.tunnelsStatusMutex.Lock()
	defer s.tunnelsStatusMutex.Unlock()
	status, ok := s.tunnelsStatus[udid]
	if !ok {
		return tunnel.Disconnected
	}

	return status
}

func (s *Service) setTunnelStatus(udid string, status tunnel.TunnelStatus) {
	s.tunnelsStatusMutex.Lock()
	defer s.tunnelsStatusMutex.Unlock()
	s.tunnelsStatus[udid] = status
	log.WithFields(log.Fields{"udid": udid, "status": status.String()}).Debug("Tunnel status updated")
}

func (s *Service) getRsd(udid string) (rsd.RsdService, bool) {
	s.rsdMapMutex.RLock()
	defer s.rsdMapMutex.RUnlock()
	r, ok := s.rsdMap[udid]
	return r, ok
}

func (s *Service) setRsd(rsd rsd.RsdService) {
	s.rsdMapMutex.Lock()
	defer s.rsdMapMutex.Unlock()
	s.rsdMap[rsd.Udid] = rsd
}

func (s *Service) rsdExists(udid string) bool {
	s.rsdMapMutex.RLock()
	defer s.rsdMapMutex.RUnlock()
	_, exists := s.rsdMap[udid]
	return exists
}

func (s *Service) removeRsd(udid string) {
	s.rsdMapMutex.Lock()
	defer s.rsdMapMutex.Unlock()
	delete(s.rsdMap, udid)
}

func (s *Service) createPrometheusMetricsResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	s.rsdMapMutex.RLock()
	defer s.rsdMapMutex.RUnlock()

	s.tunnelsMutex.RLock()
	defer s.tunnelsMutex.RUnlock()

	var sb strings.Builder

	// Auto create tunnels gauge
	autoCreateValue := 0
	if s.autoCreateTunnels {
		autoCreateValue = 1
	}
	sb.WriteString("# HELP tunneld_auto_create_tunnels Whether auto tunnel creation is enabled (1=enabled, 0=disabled)\n")
	sb.WriteString("# TYPE tunneld_auto_create_tunnels gauge\n")
	sb.WriteString(fmt.Sprintf("tunneld_auto_create_tunnels %d\n", autoCreateValue))

	// Total devices gauge
	sb.WriteString("# HELP tunneld_devices_total Total number of discovered devices\n")
	sb.WriteString("# TYPE tunneld_devices_total gauge\n")
	sb.WriteString(fmt.Sprintf("tunneld_devices_total %d\n", len(s.rsdMap)))

	// Device info metric (info-style metric with labels)
	sb.WriteString("# HELP tunneld_device_info Information about discovered devices\n")
	sb.WriteString("# TYPE tunneld_device_info gauge\n")
	for udid, rsd := range s.rsdMap {
		tunnelStatus := s.getTunnelStatus(udid)
		paired := s.isPaired(udid)
		pairedStr := "false"
		if paired {
			pairedStr = "true"
		}
		sb.WriteString(fmt.Sprintf("tunneld_device_info{udid=%q,version=%q,paired=%q,tunnel_status=%q} 1\n",
			rsd.Udid, rsd.DeviceIosVersion, pairedStr, tunnelStatus.String()))
	}

	// Paired devices gauge
	sb.WriteString("# HELP tunneld_devices_paired_total Total number of paired devices\n")
	sb.WriteString("# TYPE tunneld_devices_paired_total gauge\n")
	pairedCount := 0
	for udid := range s.rsdMap {
		if s.isPaired(udid) {
			pairedCount++
		}
	}
	sb.WriteString(fmt.Sprintf("tunneld_devices_paired_total %d\n", pairedCount))

	// Connected tunnels gauge
	sb.WriteString("# HELP tunneld_tunnels_connected_total Total number of connected tunnels\n")
	sb.WriteString("# TYPE tunneld_tunnels_connected_total gauge\n")
	connectedCount := 0
	for udid := range s.rsdMap {
		if s.getTunnelStatus(udid) == tunnel.Connected {
			connectedCount++
		}
	}
	sb.WriteString(fmt.Sprintf("tunneld_tunnels_connected_total %d\n", connectedCount))

	// Tunnel status by device
	sb.WriteString("# HELP tunneld_tunnel_status Tunnel status by device (1 if in this status, 0 otherwise)\n")
	sb.WriteString("# TYPE tunneld_tunnel_status gauge\n")
	statusValues := []tunnel.TunnelStatus{
		tunnel.Disconnected,
		tunnel.ConnectingToDevice,
		tunnel.VerifyingPairing,
		tunnel.Pairing,
		tunnel.Paired,
		tunnel.Failed,
		tunnel.Connected,
	}
	for udid := range s.rsdMap {
		currentStatus := s.getTunnelStatus(udid)
		for _, status := range statusValues {
			value := 0
			if currentStatus == status {
				value = 1
			}
			sb.WriteString(fmt.Sprintf("tunneld_tunnel_status{udid=%q,status=%q} %d\n",
				udid, status.String(), value))
		}
	}

	w.Write([]byte(sb.String()))
}

func (s *Service) createTunnelStatusResponse(w http.ResponseWriter, r *http.Request) {

	w.Header().Add("Content-Type", "application/json")
	enc := json.NewEncoder(w)

	s.rsdMapMutex.RLock()
	defer s.rsdMapMutex.RUnlock()

	s.tunnelsMutex.RLock()
	defer s.tunnelsMutex.RUnlock()

	status := struct {
		AutoCreateTunnels bool               `json:"autoCreateTunnels"`
		Devices           []DeviceTunnelInfo `json:"devices"`
	}{
		AutoCreateTunnels: s.autoCreateTunnels,
		Devices:           make([]DeviceTunnelInfo, 0),
	}

	for udid, rsd := range s.rsdMap {
		tunnelStatus := s.getTunnelStatus(udid)
		paired := s.isPaired(udid)

		var tunnelInfo *tunnel.Tunnel
		if t, ok := s.tunnels[udid]; ok {
			tunnelInfo = t
		} else {
			tunnelInfo = nil
		}

		status.Devices = append(status.Devices, DeviceTunnelInfo{
			Udid:         rsd.Udid,
			Version:      rsd.DeviceIosVersion,
			Paired:       paired,
			TunnelStatus: tunnelStatus.String(),
			TunnelInfo:   tunnelInfo,
		})
	}

	err := enc.Encode(status)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode status info: %v", err), http.StatusInternalServerError)
		return
	}
}
