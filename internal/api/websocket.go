package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/coder/websocket"
	"github.com/dmdmdm-nz/tunneld/internal/tunnel"
)

type WebSocketTunnelInfo struct {
	Status   string                  `json:"status"`
	Address  string                  `json:"address"`
	RsdPort  int                     `json:"rsdPort"`
	Services []tunnel.RsdServiceInfo `json:"services"`
}

type CreateTunnelResponse struct {
	tunnel *tunnel.Tunnel
	error  error
}

func accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, context.Context, error) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return nil, nil, err
	}
	return c, r.Context(), nil
}

func PauseRemoteD(s *Service, w http.ResponseWriter, r *http.Request) {
	c, ctx, err := accept(w, r)
	if err != nil {
		log.Error("Failed to accept client:", err)
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "closing")

	resumeRemoted, err := tunnel.SuspendRemoted()
	if err != nil {
		log.Errorf("Failed to pause RemoteD: %v", err)
		return
	}

	defer resumeRemoted()

	for {
		_, _, err := c.Read(ctx)
		if err != nil {
			return
		}
	}
}

func CreateWebSocketTunnel(s *Service, udid string, w http.ResponseWriter, r *http.Request) {
	c, ctx, err := accept(w, r)
	if err != nil {
		log.Println("accept:", err)
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "closing")

	// Create a new context that we can cancel.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	rsd, found := s.rsdMap[udid]
	if !found {
		response := WebSocketTunnelInfo{
			Status: "No such device",
		}
		b, _ := json.Marshal(response)
		_ = c.Write(ctx, websocket.MessageText, b)
		return
	}

	reqCh := make(chan CreateTunnelResponse, 1)
	go func() {
		t, err := s.createTunnel(ctx, rsd)
		reqCh <- CreateTunnelResponse{tunnel: t, error: err}
	}()

	go func() {
		_, _, err := c.Read(ctx)
		if err != nil {
			cancel()
			log.WithField("udid", udid).Info("WebSocket connection closed by client")
		}
	}()

	var tunnel *tunnel.Tunnel
	select {
	case <-ctx.Done():
		// WebSocket connection terminated before the tunnel was created
		s.removeTunnel(rsd)
		return
	case result := <-reqCh:
		if result.error != nil {
			http.Error(w, fmt.Sprintf("Failed to create tunnel: %v", result.error), http.StatusInternalServerError)
			return
		}
		tunnel = result.tunnel
	}

	response := WebSocketTunnelInfo{
		Status:   "Success",
		Address:  tunnel.Address,
		RsdPort:  tunnel.RsdPort,
		Services: tunnel.Services,
	}
	b, _ := json.Marshal(response)
	_ = c.Write(ctx, websocket.MessageText, b)

	errCh := make(chan error, 1)
	go func() {
		for {
			_, _, err := c.Read(ctx)
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			s.removeTunnel(rsd)
			return
		case <-tunnel.TunnelContext.Done():
			s.removeTunnel(rsd)
			return
		case err := <-errCh:
			if err != nil {
				s.removeTunnel(rsd)
			}
			return
		}
	}
}
