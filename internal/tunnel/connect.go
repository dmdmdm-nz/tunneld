package tunnel

import (
	"fmt"
	"net"
	"time"

	"github.com/dmdmdm-nz/tunneld/internal/tunnel/http"
	"github.com/dmdmdm-nz/tunneld/internal/tunnel/xpc"
)

func CreateXpcConnection(h *http.HttpConnection) (*xpc.Connection, error) {
	err := initializeXpcConnection(h)
	if err != nil {
		return nil, fmt.Errorf("CreateXpcConnection: failed to initialize xpc connection: %w", err)
	}

	clientServerChannel := http.NewStreamReadWriter(h, http.ClientServer)
	serverClientChannel := http.NewStreamReadWriter(h, http.ServerClient)

	xpcConn, err := xpc.New(clientServerChannel, serverClientChannel, h)
	if err != nil {
		return nil, fmt.Errorf("CreateXpcConnection: failed to create xpc connection: %w", err)
	}

	return xpcConn, nil
}

func initializeXpcConnection(h *http.HttpConnection) error {
	csWriter := http.NewStreamReadWriter(h, http.ClientServer)
	ssWriter := http.NewStreamReadWriter(h, http.ServerClient)

	err := xpc.EncodeMessage(csWriter, xpc.Message{
		Flags: xpc.AlwaysSetFlag,
		Body:  map[string]interface{}{},
		Id:    0,
	})
	if err != nil {
		return fmt.Errorf("initializeXpcConnection: failed to encode message: %w", err)
	}

	_, err = xpc.DecodeMessage(csWriter) // TODO : figure out if need to act on this frame
	if err != nil {
		return fmt.Errorf("initializeXpcConnection: failed to decode message: %w", err)
	}

	err = xpc.EncodeMessage(ssWriter, xpc.Message{
		Flags: xpc.InitHandshakeFlag | xpc.AlwaysSetFlag,
		Body:  nil,
		Id:    0,
	})
	if err != nil {
		return fmt.Errorf("initializeXpcConnection: failed to encode message 2: %w", err)
	}

	_, err = xpc.DecodeMessage(ssWriter) // TODO : figure out if need to act on this frame
	if err != nil {
		return fmt.Errorf("initializeXpcConnection: failed to decode message 2: %w", err)
	}

	err = xpc.EncodeMessage(csWriter, xpc.Message{
		Flags: 0x201, // alwaysSetFlag | 0x200
		Body:  nil,
		Id:    0,
	})
	if err != nil {
		return fmt.Errorf("initializeXpcConnection: failed to encode message 3: %w", err)
	}

	_, err = xpc.DecodeMessage(csWriter) // TODO : figure out if need to act on this frame
	if err != nil {
		return fmt.Errorf("initializeXpcConnection: failed to decode message 3: %w", err)
	}

	return nil
}

// connect to a operating system level TUN device
func ConnectTUN(address string, port int) (*net.TCPConn, error) {
	addr, err := net.ResolveTCPAddr("tcp6", fmt.Sprintf("[%s]:%d", address, port))
	if err != nil {
		return nil, fmt.Errorf("ConnectToHttp2WithAddr: failed to resolve address: %w", err)
	}
	conn, err := net.DialTCP("tcp", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("ConnectToHttp2WithAddr: failed to dial: %w", err)
	}
	err = conn.SetKeepAlive(true)
	if err != nil {
		return nil, fmt.Errorf("ConnectUserSpaceTunnel: failed to set keepalive: %w", err)
	}
	err = conn.SetKeepAlivePeriod(1 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("ConnectUserSpaceTunnel: failed to set keepalive period: %w", err)
	}

	return conn, nil
}
