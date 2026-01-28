# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

TunnelD is a Go service that creates RSD (Remote Service Discovery) tunnels to iOS 17+ devices over network interfaces. It uses NCM TCP tunnels (not USBMUX) and supports Linux and macOS only.

## Build Commands

```bash
make build-macos-arm64    # macOS Apple Silicon
make build-linux-x64      # Linux x86_64
make build-linux-arm64    # Linux ARM64
make build-all            # All platforms
make clean                # Remove build artifacts
```

The build injects version info via ldflags (`pkg/version`).

## Running

Requires root privileges for network interface and remoted service management.

```bash
sudo ./tunneld -auto-create-tunnels    # Auto-create tunnels on device discovery
sudo ./tunneld                          # Manual tunnel creation via API
```

Key flags: `-port` (default 60105), `-host` (default 127.0.0.1), `-log-level`

## Architecture

Three services run in a dependency chain, orchestrated by a Supervisor:

```
NetMon → RSD → API
```

### Service Flow

1. **NetMon** (`internal/netmon/`) - Listens for network interface changes using platform-specific event mechanisms (netlink on Linux, AF_ROUTE sockets on macOS), publishes `InterfaceAdded`/`InterfaceRemoved` events for "en*" interfaces with IPv6 addresses

2. **RSD** (`internal/rsd/`) - Subscribes to NetMon, discovers iOS devices via mDNS (`_remoted._tcp.local.`), retrieves device UDID/version, publishes `RsdServiceAdded`/`RsdServiceRemoved` events

3. **API** (`internal/api/`) - HTTP server subscribing to RSD events, manages tunnel lifecycle, exposes REST/WebSocket endpoints

### Tunnel Implementation

`internal/tunnel/` contains the core protocol implementation:
- TLS connections and handshake protocol
- Pairing record management (`pair-record.go`)
- TUN device creation via `songgao/water`
- HTTP/2 multiplexing (`http/`)
- XPC protocol encoding (`xpc/`)
- System remoted service control (`remoted.go`)

### Subscription Pattern

All services use a pub/sub pattern (`internal/runtime/subqueue.go`):
- New subscribers receive current state as snapshot (synthetic "Added" events)
- Then receive live events
- Prevents race conditions between subscription and service startup

### Key Dependencies

- `github.com/dmdmdm-nz/zeroconf` - Custom mDNS fork for RSD discovery
- `github.com/songgao/water` - TUN device creation
- `github.com/vishvananda/netlink` - Linux netlink for interface monitoring
- `howett.net/plist` - Apple plist parsing

## API Endpoints

Go-iOS compatible: `/health`, `/ready`, `/shutdown`, `/tunnel/<udid>`, `/tunnels`

Additional: `/status`, `/metrics` (Prometheus), `/services/<udid>`

WebSocket: `/ws/create-tunnel?udid=<udid>`, `/ws/pause-remoted`
