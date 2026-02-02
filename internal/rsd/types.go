package rsd

type EventType string

const (
	RsdServiceAdded   EventType = "SERVICE_ADDED"
	RsdServiceRemoved EventType = "SERVICE_REMOVED"
)

type RsdService struct {
	Udid             string
	InterfaceName    string
	Address          string
	DeviceIosVersion string
	Services         map[string]ServiceEntry
}

// ServiceEntry represents a service available on the device.
type ServiceEntry struct {
	Port uint32
}

type RsdServiceEvent struct {
	Type EventType
	Info RsdService
}

type EventHandler func(event RsdServiceEvent)
