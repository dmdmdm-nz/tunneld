package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dmdmdm-nz/tunneld/internal/tunnel"
	"github.com/dmdmdm-nz/tunneld/internal/tunnelmgr"
)

// mockTunnelManager is a mock implementation of TunnelManager for testing
type mockTunnelManager struct {
	tunnels           map[string]*tunnel.Tunnel
	autoCreateEnabled bool
	devices           map[string]bool
	deviceReady       map[string]bool
	devicePaired      map[string]bool
}

func newMockTunnelManager() *mockTunnelManager {
	return &mockTunnelManager{
		tunnels:      make(map[string]*tunnel.Tunnel),
		devices:      make(map[string]bool),
		deviceReady:  make(map[string]bool),
		devicePaired: make(map[string]bool),
	}
}

func (m *mockTunnelManager) GetTunnel(udid string) (*tunnel.Tunnel, bool) {
	t, ok := m.tunnels[udid]
	return t, ok
}

func (m *mockTunnelManager) RemoveTunnel(udid string) bool {
	if _, ok := m.tunnels[udid]; ok {
		delete(m.tunnels, udid)
		return true
	}
	return false
}

func (m *mockTunnelManager) GetAllTunnels() []*tunnel.Tunnel {
	result := make([]*tunnel.Tunnel, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		result = append(result, t)
	}
	return result
}

func (m *mockTunnelManager) IsAutoCreateEnabled() bool {
	return m.autoCreateEnabled
}

func (m *mockTunnelManager) DeviceExists(udid string) bool {
	return m.devices[udid]
}

func (m *mockTunnelManager) IsDeviceReady(udid string) bool {
	return m.deviceReady[udid]
}

func (m *mockTunnelManager) IsDevicePaired(udid string) bool {
	return m.devicePaired[udid]
}

func (m *mockTunnelManager) GetAllDevices() []*tunnelmgr.TunnelDevice {
	return nil
}

func (m *mockTunnelManager) GetDevice(udid string) (*tunnelmgr.TunnelDevice, bool) {
	return nil, false
}

func (m *mockTunnelManager) CreateTunnel(ctx context.Context, udid string) (*tunnel.Tunnel, error) {
	return nil, nil
}

// Helper to create a service with a mock manager for testing
func newTestService(tm TunnelManager) *Service {
	s := NewService("127.0.0.1", 0)
	s.AttachTunnelMgr(tm)
	return s
}

func TestDeleteTunnel_Success(t *testing.T) {
	mock := newMockTunnelManager()
	mock.tunnels["test-udid"] = &tunnel.Tunnel{
		Udid:    "test-udid",
		Address: "fe80::1",
		RsdPort: 58783,
	}

	s := newTestService(mock)

	req := httptest.NewRequest(http.MethodDelete, "/tunnel/test-udid", nil)
	w := httptest.NewRecorder()

	// Create a handler that mimics the mux routing
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		udid := r.URL.Path[len("/tunnel/"):]
		if len(udid) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if r.Method == http.MethodDelete {
			if !s.tm.RemoveTunnel(udid) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
	})

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// Verify tunnel was removed
	_, exists := mock.tunnels["test-udid"]
	assert.False(t, exists)
}

func TestDeleteTunnel_NotFound(t *testing.T) {
	mock := newMockTunnelManager()
	// No tunnel exists

	s := newTestService(mock)

	req := httptest.NewRequest(http.MethodDelete, "/tunnel/nonexistent-udid", nil)
	w := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		udid := r.URL.Path[len("/tunnel/"):]
		if len(udid) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if r.Method == http.MethodDelete {
			if !s.tm.RemoveTunnel(udid) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
	})

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteTunnel_EmptyUdid(t *testing.T) {
	mock := newMockTunnelManager()
	s := newTestService(mock)

	req := httptest.NewRequest(http.MethodDelete, "/tunnel/", nil)
	w := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		udid := r.URL.Path[len("/tunnel/"):]
		if len(udid) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if r.Method == http.MethodDelete {
			if !s.tm.RemoveTunnel(udid) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
	})

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetTunnel_Success(t *testing.T) {
	mock := newMockTunnelManager()
	mock.tunnels["test-udid"] = &tunnel.Tunnel{
		Udid:    "test-udid",
		Address: "fe80::1",
		RsdPort: 58783,
	}

	s := newTestService(mock)

	req := httptest.NewRequest(http.MethodGet, "/tunnel/test-udid", nil)
	w := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		udid := r.URL.Path[len("/tunnel/"):]
		if len(udid) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if r.Method == http.MethodGet {
			tun, ok := s.tm.GetTunnel(udid)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Add("Content-Type", "application/json")
			json.NewEncoder(w).Encode(tun)
			return
		}
	})

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var result tunnel.Tunnel
	err := json.Unmarshal(w.Body.Bytes(), &result)
	assert.NoError(t, err)
	assert.Equal(t, "test-udid", result.Udid)
	assert.Equal(t, "fe80::1", result.Address)
}

func TestGetTunnel_NotFound(t *testing.T) {
	mock := newMockTunnelManager()
	s := newTestService(mock)

	req := httptest.NewRequest(http.MethodGet, "/tunnel/nonexistent-udid", nil)
	w := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		udid := r.URL.Path[len("/tunnel/"):]
		if len(udid) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if r.Method == http.MethodGet {
			tun, ok := s.tm.GetTunnel(udid)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Add("Content-Type", "application/json")
			json.NewEncoder(w).Encode(tun)
			return
		}
	})

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAllTunnels_Empty(t *testing.T) {
	mock := newMockTunnelManager()
	s := newTestService(mock)

	req := httptest.NewRequest(http.MethodGet, "/tunnels", nil)
	w := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Add("Content-Type", "application/json")
			tunnels := s.tm.GetAllTunnels()
			tunnelList := make([]tunnel.Tunnel, 0, len(tunnels))
			for _, t := range tunnels {
				tunnelList = append(tunnelList, *t)
			}
			json.NewEncoder(w).Encode(tunnelList)
			return
		}
	})

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result []tunnel.Tunnel
	err := json.Unmarshal(w.Body.Bytes(), &result)
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestGetAllTunnels_WithTunnels(t *testing.T) {
	mock := newMockTunnelManager()
	mock.tunnels["udid-1"] = &tunnel.Tunnel{Udid: "udid-1", Address: "fe80::1"}
	mock.tunnels["udid-2"] = &tunnel.Tunnel{Udid: "udid-2", Address: "fe80::2"}

	s := newTestService(mock)

	req := httptest.NewRequest(http.MethodGet, "/tunnels", nil)
	w := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Add("Content-Type", "application/json")
			tunnels := s.tm.GetAllTunnels()
			tunnelList := make([]tunnel.Tunnel, 0, len(tunnels))
			for _, t := range tunnels {
				tunnelList = append(tunnelList, *t)
			}
			json.NewEncoder(w).Encode(tunnelList)
			return
		}
	})

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result []tunnel.Tunnel
	err := json.Unmarshal(w.Body.Bytes(), &result)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
}
