package tunnel

type TunnelStatus int

func (t TunnelStatus) String() string {
	switch t {
	case Connecting:
		return "Connecting"
	case Pairing:
		return "Pairing"
	case Failed:
		return "Failed"
	case Connected:
		return "Connected"
	case Disconnected:
		return "Disconnected"
	default:
		return "Unknown"
	}
}

const (
	Connecting TunnelStatus = iota
	Pairing
	Failed
	Connected
	Disconnected
)
