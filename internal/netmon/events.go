package netmon

type EventType string

const (
	InterfaceAdded   EventType = "INTERFACE_ADDED"
	InterfaceRemoved EventType = "INTERFACE_REMOVED"
)

type InterfaceEvent struct {
	Type          EventType
	InterfaceName string
}

type EventHandler func(event InterfaceEvent)
