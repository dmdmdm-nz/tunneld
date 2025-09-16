# TunnelD

Creates RSD tunnels to iOS 17+ devices. 

Based off Go-iOS's (https://github.com/danielpaulus/go-ios) tunnel service but with a few differences:

- Uses the same tunnel type for all iOS 17+ devices (NCM TCP)
- Doesn't use USBMUX
- Supports on-demand tunnels via WebSocket request
- Only supports Linux and MacOS (no support for Windows)

## Getting started

```
% ./tunneld
Usage of ./tunneld:
  -auto-create-tunnels
        Automatically create tunnels for new network interfaces
  -host string
        Host to bind to (default "127.0.0.1")
  -log-level string
        Log level (trace, debug, info, warn, error) (default "info")
  -poll-interval int
        Interface poll interval in seconds (default 3)
  -port int
        Port to listen on (default 60105)
  -version
        Show version information
```

## API

Go-iOS compatible API:
```
GET /health
GET /ready
GET /shutdown
GET /tunnel/<udid>
DELETE /tunnel/<udid>
GET /tunnels
```

WebSocket On-demand API:

```
GET /ws/create-tunnel?udid=<udid>
```