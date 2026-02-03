package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/dmdmdm-nz/tunneld/internal/tunnel"
	"github.com/dmdmdm-nz/tunneld/internal/tunnelmgr"
)

// TunnelManager defines the interface for tunnel management operations
type TunnelManager interface {
	GetTunnel(udid string) (*tunnel.Tunnel, bool)
	RemoveTunnel(udid string) bool
	GetAllTunnels() []*tunnel.Tunnel
	IsAutoCreateEnabled() bool
	DeviceExists(udid string) bool
	IsDeviceReady(udid string) bool
	IsDevicePaired(udid string) bool
	GetAllDevices() []*tunnelmgr.TunnelDevice
	GetDevice(udid string) (*tunnelmgr.TunnelDevice, bool)
	CreateTunnel(ctx context.Context, udid string) (*tunnel.Tunnel, error)
}

// Service represents the HTTP server for the API
type Service struct {
	address string
	port    int
	tm      TunnelManager
}

func NewService(host string, port int) *Service {
	return &Service{
		address: host,
		port:    port,
	}
}

func (s *Service) AttachTunnelMgr(tm TunnelManager) {
	s.tm = tm
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

	<-ctx.Done()
	return nil
}

func (s *Service) Close() error {
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
			tunnel, ok := s.tm.GetTunnel(udid)
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
			if !s.tm.RemoveTunnel(udid) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
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
			tunnel, ok := s.tm.GetTunnel(udid)
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

			tunnels := s.tm.GetAllTunnels()
			tunnelList := make([]tunnel.Tunnel, 0, len(tunnels))
			for _, t := range tunnels {
				tunnelList = append(tunnelList, *t)
			}
			err := enc.Encode(tunnelList)
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

			if s.tm.IsAutoCreateEnabled() {
				http.Error(w, "Cannot manually create tunnels when autoCreateTunnels is enabled.", http.StatusMethodNotAllowed)
				return
			}

			if !s.tm.DeviceExists(udid) {
				http.Error(w, "No RSD service found for the given UDID", http.StatusNotFound)
				return
			}

			if !s.tm.IsDeviceReady(udid) {
				http.Error(w, "Device is not ready", http.StatusNotFound)
				return
			}

			if !s.tm.IsDevicePaired(udid) {
				http.Error(w, "Device has not been paired", http.StatusConflict)
				return
			}

			CreateWebSocketTunnel(s, udid, w, r)
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

func (s *Service) createPrometheusMetricsResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	devices := s.tm.GetAllDevices()

	var sb strings.Builder

	// Auto create tunnels gauge
	autoCreateValue := 0
	if s.tm.IsAutoCreateEnabled() {
		autoCreateValue = 1
	}
	sb.WriteString("# HELP tunneld_auto_create_tunnels Whether auto tunnel creation is enabled (1=enabled, 0=disabled)\n")
	sb.WriteString("# TYPE tunneld_auto_create_tunnels gauge\n")
	sb.WriteString(fmt.Sprintf("tunneld_auto_create_tunnels %d\n", autoCreateValue))

	// Total devices gauge
	sb.WriteString("# HELP tunneld_devices_total Total number of discovered devices\n")
	sb.WriteString("# TYPE tunneld_devices_total gauge\n")
	sb.WriteString(fmt.Sprintf("tunneld_devices_total %d\n", len(devices)))

	// Device info metric (info-style metric with labels)
	sb.WriteString("# HELP tunneld_device_info Information about discovered devices\n")
	sb.WriteString("# TYPE tunneld_device_info gauge\n")
	for _, device := range devices {
		rsdInfo := device.GetRsdInfo()
		tunnelStatus := device.GetStatus()
		paired := device.IsPaired()
		pairedStr := "false"
		if paired {
			pairedStr = "true"
		}
		sb.WriteString(fmt.Sprintf("tunneld_device_info{udid=%q,version=%q,paired=%q,tunnel_status=%q} 1\n",
			rsdInfo.Udid, rsdInfo.DeviceIosVersion, pairedStr, tunnelStatus.String()))
	}

	// Paired devices gauge
	sb.WriteString("# HELP tunneld_devices_paired_total Total number of paired devices\n")
	sb.WriteString("# TYPE tunneld_devices_paired_total gauge\n")
	pairedCount := 0
	for _, device := range devices {
		if device.IsPaired() {
			pairedCount++
		}
	}
	sb.WriteString(fmt.Sprintf("tunneld_devices_paired_total %d\n", pairedCount))

	// Connected tunnels gauge
	sb.WriteString("# HELP tunneld_tunnels_connected_total Total number of connected tunnels\n")
	sb.WriteString("# TYPE tunneld_tunnels_connected_total gauge\n")
	connectedCount := 0
	for _, device := range devices {
		if device.GetStatus() == tunnel.Connected {
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
	for _, device := range devices {
		udid := device.GetUdid()
		currentStatus := device.GetStatus()
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

	devices := s.tm.GetAllDevices()

	status := struct {
		AutoCreateTunnels bool               `json:"autoCreateTunnels"`
		Devices           []DeviceTunnelInfo `json:"devices"`
	}{
		AutoCreateTunnels: s.tm.IsAutoCreateEnabled(),
		Devices:           make([]DeviceTunnelInfo, 0),
	}

	for _, device := range devices {
		rsdInfo := device.GetRsdInfo()
		tunnelStatus := device.GetStatus()
		paired := device.IsPaired()

		var tunnelInfo *tunnel.Tunnel
		if t, ok := device.GetTunnel(); ok {
			tunnelInfo = t
		}

		status.Devices = append(status.Devices, DeviceTunnelInfo{
			Udid:         rsdInfo.Udid,
			Version:      rsdInfo.DeviceIosVersion,
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
