package tunnel

import (
	"sync"

	"github.com/dmdmdm-nz/tunneld/internal/runtime"
)

type TunnelNotifications struct {
	subsMu sync.Mutex
	subs   map[int]*runtime.SubQueue[TunnelEvent]
	nextID int

	closed bool
}

func NewTunnelNotifications() *TunnelNotifications {
	return &TunnelNotifications{
		subs: make(map[int]*runtime.SubQueue[TunnelEvent]),
	}
}

// Subscribe follows the same "snapshot as Adds, then live" pattern.
func (tn *TunnelNotifications) Subscribe() (<-chan TunnelEvent, func()) {
	sub := runtime.NewSubQueue[TunnelEvent](8)

	// Register paused
	tn.subsMu.Lock()
	id := tn.nextID
	tn.nextID++
	tn.subs[id] = sub
	tn.subsMu.Unlock()

	// Go live
	sub.SetPaused(false)

	unsub := func() {
		tn.subsMu.Lock()
		if q, ok := tn.subs[id]; ok {
			delete(tn.subs, id)
			q.Close()
		}
		tn.subsMu.Unlock()
	}
	return sub.Chan(), unsub
}

func (tn *TunnelNotifications) Close() error {

	tn.subsMu.Lock()
	defer tn.subsMu.Unlock()
	if tn.closed {
		return nil
	}
	tn.closed = true
	for id, q := range tn.subs {
		q.Close()
		delete(tn.subs, id)
	}
	return nil
}

func (tn *TunnelNotifications) broadcast(ev TunnelEvent) {
	tn.subsMu.Lock()
	defer tn.subsMu.Unlock()
	for _, sub := range tn.subs {
		sub.Enqueue(ev)
	}
}

func (tn *TunnelNotifications) NotifyTunnelStatus(udid string, status TunnelStatus) {
	tn.broadcast(TunnelEvent{
		Type:   TunnelProgress,
		Udid:   udid,
		Status: status,
	})
}

func (tn *TunnelNotifications) NotifyDevicePaired(udid string) {
	tn.broadcast(TunnelEvent{
		Type:   DevicePaired,
		Udid:   udid,
		Status: Paired,
	})
}

func (tn *TunnelNotifications) NotifyDeviceNotPaired(udid string) {
	tn.broadcast(TunnelEvent{
		Type:   DeviceNotPaired,
		Udid:   udid,
		Status: Disconnected,
	})
}
