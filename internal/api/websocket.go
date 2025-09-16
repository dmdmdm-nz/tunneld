package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/coder/websocket"
)

type WebSocketTunnelInfo struct {
	Status  string `json:"status"`
	Address string `json:"address"`
	RsdPort int    `json:"rsdPort"`
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

func CreateWebSocketTunnel(s *Service, udid string, w http.ResponseWriter, r *http.Request) {
	c, ctx, err := accept(w, r)
	if err != nil {
		log.Println("accept:", err)
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "closing")

	rsd, found := s.rsdMap[udid]
	if !found {
		response := WebSocketTunnelInfo{
			Status: "No such device",
		}
		b, _ := json.Marshal(response)
		_ = c.Write(ctx, websocket.MessageText, b)
		return
	}

	tunnel, err := s.createTunnel(ctx, rsd)
	if err != nil {
		response := WebSocketTunnelInfo{
			Status: "Failed to create tunnel",
		}
		b, _ := json.Marshal(response)
		_ = c.Write(ctx, websocket.MessageText, b)
		return
	}

	response := WebSocketTunnelInfo{
		Status:  "Success",
		Address: tunnel.Address,
		RsdPort: tunnel.RsdPort,
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
