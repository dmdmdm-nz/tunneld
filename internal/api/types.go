package api

import "github.com/dmdmdm-nz/tunneld/internal/tunnel"

type DeviceTunnelInfo struct {
	Udid         string         `json:"udid"`
	TunnelStatus string         `json:"tunnelStatus"`
	TunnelInfo   *tunnel.Tunnel `json:"tunnelInfo"`
}
