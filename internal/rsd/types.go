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
}

type RsdServiceEvent struct {
	Type EventType
	Info RsdService
}

type EventHandler func(event RsdServiceEvent)
