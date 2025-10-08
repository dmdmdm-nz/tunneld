package tunnel

type TunnelStatus int

func (t TunnelStatus) String() string {
	switch t {
	case Disconnected:
		return "Disconnected"
	case ConnectingToDevice:
		return "ConnectingToDevice"
	case VerifyingPairing:
		return "VerifyingPairing"
	case Pairing:
		return "Pairing"
	case Paired:
		return "ConnectingToTunnel"
	case Failed:
		return "Failed"
	case Connected:
		return "Connected"
	default:
		return "Unknown"
	}
}

const (
	Disconnected TunnelStatus = iota
	ConnectingToDevice
	VerifyingPairing
	Pairing
	Paired
	Failed
	Connected
)

type TunnelEventType string

const (
	DeviceNotPaired TunnelEventType = "DEVICE_NOT_PAIRED"
	DevicePaired    TunnelEventType = "DEVICE_PAIRED"
	TunnelProgress  TunnelEventType = "TUNNEL_PROGRESS"
)

type TunnelEvent struct {
	Type   TunnelEventType
	Udid   string
	Status TunnelStatus
}

type EventHandler func(event TunnelEvent)
