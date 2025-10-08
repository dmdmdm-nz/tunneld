package tunnel

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"

	"github.com/dmdmdm-nz/tunneld/internal/tls"
	"github.com/dmdmdm-nz/tunneld/internal/tunnel/http"

	log "github.com/sirupsen/logrus"
	"github.com/songgao/water"
)

const TUNNEL_MTU = 64_000

// Tunnel describes the parameters of an established tunnel to the device
type Tunnel struct {
	Address          string          `json:"address"`          // Address is the IPv6 address of the device over the tunnel
	RsdPort          int             `json:"rsdPort"`          // RsdPort is the port on which remote service discover is reachable
	Udid             string          `json:"udid"`             // Udid is the id of the device for this tunnel
	UserspaceTUN     bool            `json:"userspaceTun"`     // Always false
	UserspaceTUNPort int             `json:"userspaceTunPort"` // Always 0
	TunnelContext    context.Context `json:"-"`
	closer           func() error    `json:"-"`
}

// Close closes the connection to the device and removes the virtual network interface from the host
func (t Tunnel) Close() error {
	return t.closer()
}

// ManualPairAndConnectToTunnel tries to verify an existing pairing, and if this fails it triggers a new manual pairing process.
// After a successful pairing a tunnel for this device gets started and the tunnel information is returned
func ManualPairAndConnectToTunnel(ctx context.Context, p *PairRecordManager, addr string, udid string, autoPair bool, tn *TunnelNotifications) (Tunnel, error) {
	tn.NotifyTunnelStatus(udid, ConnectingToDevice)

	resumeRemoted, err := SuspendRemoted()
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return Tunnel{}, fmt.Errorf("ManualPairAndConnectToTunnel: failed to suspend remoted: %w", err)
	}
	defer resumeRemoted()

	port, err := getUntrustedTunnelServicePort(addr)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return Tunnel{}, fmt.Errorf("ManualPairAndConnectToTunnel: could not find port for '%s'", untrustedTunnelServiceName)
	}
	conn, err := ConnectTUN(addr, port)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return Tunnel{}, fmt.Errorf("ManualPairAndConnectToTunnel: failed to connect to TUN device: %w", err)
	}
	defer conn.Close()

	h, err := http.NewHttpConnection(conn)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return Tunnel{}, fmt.Errorf("ManualPairAndConnectToTunnel: failed to create HTTP2 connection: %w", err)
	}

	xpcConn, err := CreateXpcConnection(h)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return Tunnel{}, fmt.Errorf("ManualPairAndConnectToTunnel: failed to create RemoteXPC connection: %w", err)
	}
	ts := newTunnelServiceWithXpc(xpcConn, h, p)

	tn.NotifyTunnelStatus(udid, VerifyingPairing)
	pairingResult, err := ts.VerifyPairing(ctx, udid)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return Tunnel{}, fmt.Errorf("ManualPairAndConnectToTunnel: failed to verify pairing: %w", err)
	}

	if pairingResult.NeedsPairing {
		tn.NotifyDeviceNotPaired(udid)

		if autoPair {
			tn.NotifyTunnelStatus(udid, Pairing)
			pairingResult, err = ts.ManualPair(ctx, udid, pairingResult.SharedSecret)
			if err == nil {
				// We need to re-establish the connection after a new pairing.
				tn.NotifyTunnelStatus(udid, Disconnected)
				return Tunnel{}, errors.New("ManualPairAndConnectToTunnel: new pairing created, re-attempting connection")
			} else {
				tn.NotifyTunnelStatus(udid, Failed)
				return Tunnel{}, fmt.Errorf("ManualPairAndConnectToTunnel: pairing failed: %w", err)
			}
		} else {
			tn.NotifyTunnelStatus(udid, Failed)
			return Tunnel{}, errors.New("ManualPairAndConnectToTunnel: device not paired")
		}
	}

	tn.NotifyDevicePaired(udid)
	tn.NotifyTunnelStatus(udid, Paired)

	tunnelInfo, err := ts.createTunnelListener(pairingResult.SharedSecret)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return Tunnel{}, fmt.Errorf("ManualPairAndConnectToTunnel: failed to create tunnel listener: %w", err)
	}
	t, err := connectToTunnel(ctx, tunnelInfo, addr, udid, pairingResult.SharedSecret)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return Tunnel{}, fmt.Errorf("ManualPairAndConnectToTunnel: failed to connect to tunnel: %w", err)
	}

	tn.NotifyTunnelStatus(udid, Connected)
	return t, nil
}

// ManualPair tries to verify an existing pairing, and if this fails it triggers a new manual pairing process.
func ManualPair(ctx context.Context, p *PairRecordManager, addr string, udid string, tn *TunnelNotifications) error {
	tn.NotifyTunnelStatus(udid, ConnectingToDevice)

	resumeRemoted, err := SuspendRemoted()
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return fmt.Errorf("ManualPair: failed to suspend remoted: %w", err)
	}
	defer resumeRemoted()

	port, err := getUntrustedTunnelServicePort(addr)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return fmt.Errorf("ManualPair: could not find port for '%s'", untrustedTunnelServiceName)
	}
	conn, err := ConnectTUN(addr, port)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return fmt.Errorf("ManualPair: failed to connect to TUN device: %w", err)
	}
	defer conn.Close()

	h, err := http.NewHttpConnection(conn)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return fmt.Errorf("ManualPair: failed to create HTTP2 connection: %w", err)
	}

	xpcConn, err := CreateXpcConnection(h)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return fmt.Errorf("ManualPair: failed to create RemoteXPC connection: %w", err)
	}
	ts := newTunnelServiceWithXpc(xpcConn, h, p)

	tn.NotifyTunnelStatus(udid, VerifyingPairing)
	pairingResult, err := ts.VerifyPairing(ctx, udid)
	if err != nil {
		tn.NotifyTunnelStatus(udid, Failed)
		return fmt.Errorf("ManualPairAndConnectToTunnel: failed to verify pairing: %w", err)
	}

	if pairingResult.NeedsPairing {
		tn.NotifyDeviceNotPaired(udid)
		tn.NotifyTunnelStatus(udid, Pairing)
		pairingResult, err = ts.ManualPair(ctx, udid, pairingResult.SharedSecret)
		if err != nil {
			tn.NotifyTunnelStatus(udid, Failed)
			return fmt.Errorf("ManualPairAndConnectToTunnel: pairing failed: %w", err)
		}
	}

	tn.NotifyDevicePaired(udid)
	tn.NotifyTunnelStatus(udid, Disconnected)
	return nil
}

func getUntrustedTunnelServicePort(addr string) (int, error) {
	rsdService, err := NewWithAddr(addr)
	if err != nil {
		return 0, fmt.Errorf("getUntrustedTunnelServicePort: failed to connect to RSD service: %w", err)
	}
	defer rsdService.Close()
	handshakeResponse, err := rsdService.Handshake()
	if err != nil {
		return 0, fmt.Errorf("getUntrustedTunnelServicePort: failed to perform RSD handshake: %w", err)
	}

	port := handshakeResponse.GetPort(untrustedTunnelServiceName)
	if port == 0 {
		log.WithField("services", maps.Values(handshakeResponse.Services)).Info("Available services")
		return 0, fmt.Errorf("getUntrustedTunnelServicePort: could not find port for '%s'", untrustedTunnelServiceName)
	}
	return port, nil
}

func connectToTunnel(ctx context.Context, info tunnelListener, addr string, udid string, sharedSecret []byte) (Tunnel, error) {
	tlsConfig, err := createTlsConfig(info, sharedSecret)
	if err != nil {
		return Tunnel{}, err
	}

	conn, err := tls.Dial("tcp", fmt.Sprintf("[%s]:%d", addr, info.TunnelPort), tlsConfig)
	if err != nil {
		return Tunnel{}, fmt.Errorf("failed to create TCP connection to tunnel: %w", err)
	}

	stream := struct {
		io.Reader
		io.Writer
		io.Closer
	}{conn, conn, conn}

	tunnelInfo, err := exchangeCoreTunnelParameters(stream)
	if err != nil {
		return Tunnel{}, fmt.Errorf("could not exchange tunnel parameters. %w", err)
	}

	utunIface, err := setupTunnelInterface(tunnelInfo)
	if err != nil {
		return Tunnel{}, fmt.Errorf("could not setup tunnel interface. %w", err)
	}

	tunnelCtx, cancel := context.WithCancel(ctx)
	closeFunc := func() error {
		cancel()
		tlsErr := conn.Close()
		utunErr := utunIface.Close()
		return errors.Join(tlsErr, utunErr)
	}

	go func() {
		err := forwardDataToInterface(tunnelCtx, tunnelInfo.ClientParameters.Mtu, utunIface, conn)
		if err != nil {
			log.WithError(err).Debug("Exiting device->tunnel data forwarder")
		}

		closeFunc()
	}()

	go func() {
		err := forwardDataToDevice(tunnelCtx, tunnelInfo.ClientParameters.Mtu, utunIface, conn)
		if err != nil {
			log.WithError(err).Debug("Exiting tunnel->device data forwarder")
		}

		closeFunc()
	}()

	return Tunnel{
		Address:       tunnelInfo.ServerAddress,
		RsdPort:       int(tunnelInfo.ServerRSDPort),
		Udid:          udid,
		TunnelContext: tunnelCtx,
		closer:        closeFunc,
	}, nil
}

func setupTunnelInterface(tunnelInfo tunnelParameters) (io.ReadWriteCloser, error) {
	ifce, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if err != nil {
		return nil, fmt.Errorf("setupTunnelInterface: failed creating TUN device %w", err)
	}

	const prefixLength = 64 // TODO: this could be calculated from the netmask provided by the device

	setIpAddr := exec.Command("ifconfig", ifce.Name(), "inet6", "add", fmt.Sprintf("%s/%d", tunnelInfo.ClientParameters.Address, prefixLength))
	err = runCmd(setIpAddr)
	if err != nil {
		return nil, fmt.Errorf("setupTunnelInterface: failed to set IP address for interface: %w", err)
	}

	enableIfce := exec.Command("ifconfig", ifce.Name(), "up")
	err = runCmd(enableIfce)
	if err != nil {
		return nil, fmt.Errorf("setupTunnelInterface: failed to enable interface %s: %w", ifce.Name(), err)
	}

	return ifce, nil
}

func runCmd(cmd *exec.Cmd) error {
	buf := new(bytes.Buffer)
	cmd.Stderr = buf
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("runCmd: failed to exeute command (stderr: %s): %w", buf.String(), err)
	}
	return nil
}

func createTlsConfig(info tunnelListener, sharedSecret []byte) (*tls.Config, error) {
	conf := &tls.Config{
		CipherSuites: []uint16{
			tls.TLS_PSK_WITH_AES_128_GCM_SHA256,
		},
		Certificates: []tls.Certificate{{}},
		MaxVersion:   tls.VersionTLS12,
		MinVersion:   tls.VersionTLS12,
		ServerName:   "",
		Extra: tls.PSKConfig{
			GetIdentity: func() string {
				return ""
			},
			GetKey: func(identity string) ([]byte, error) {
				return sharedSecret, nil
			},
		},
	}
	return conf, nil
}

func forwardDataToDevice(ctx context.Context, mtu uint64, r io.Reader, conn *tls.Conn) error {
	packet := make([]byte, mtu)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			n, err := r.Read(packet)
			if err != nil {
				return fmt.Errorf("could not read packet. %w", err)
			}

			_, err = conn.Write(packet[:n])
			if err != nil {
				return fmt.Errorf("could not write packet. %w", err)
			}
		}
	}
}

func forwardDataToInterface(ctx context.Context, mtu uint64, w io.Writer, conn *tls.Conn) error {
	packet := make([]byte, mtu)
	ipv6_header := make([]byte, 40)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:

			// It's important when writing to the TUN interface that we write a full IPv6 packet at once.
			// So we first read the IPv6 header to get the length of the packet, and then we read the rest of the packet
			// and write it to the TUN interface in one go.

			// Read the IPv6 header first (40 bytes)
			_, err := io.ReadFull(conn, ipv6_header)
			if err != nil {
				return fmt.Errorf("failed to read data. %w", err)
			}

			ipv6_length := binary.BigEndian.Uint16(ipv6_header[4:6])
			ipv6_body := make([]byte, ipv6_length)
			_, err = io.ReadFull(conn, ipv6_body[:ipv6_length])
			if err != nil {
				return fmt.Errorf("failed to read data. %w", err)
			}

			totalLen := len(ipv6_header) + int(ipv6_length)
			n := copy(packet, ipv6_header)
			copy(packet[n:], ipv6_body[:ipv6_length])

			if _, err := w.Write(packet[:totalLen]); err != nil {
				return fmt.Errorf("failed to write packet: %w", err)
			}
		}
	}
}

func exchangeCoreTunnelParameters(stream io.ReadWriteCloser) (tunnelParameters, error) {
	rq, err := json.Marshal(map[string]interface{}{
		"type": "clientHandshakeRequest",
		"mtu":  TUNNEL_MTU,
	})
	if err != nil {
		return tunnelParameters{}, err
	}

	buf := bytes.NewBuffer(nil)
	// Write on bytes.Buffer never returns an error
	_, _ = buf.Write([]byte("CDTunnel\000"))
	_ = buf.WriteByte(byte(len(rq)))
	_, _ = buf.Write(rq)

	_, err = stream.Write(buf.Bytes())
	if err != nil {
		return tunnelParameters{}, err
	}

	header := make([]byte, len("CDTunnel")+2)
	_, err = stream.Read(header)
	if err != nil {
		return tunnelParameters{}, fmt.Errorf("could not header read from stream. %w", err)
	}

	bodyLen := header[len(header)-1]

	res := make([]byte, bodyLen)
	length, err := stream.Read(res)
	if err != nil {
		return tunnelParameters{}, fmt.Errorf("could not read from stream. %w", err)
	}

	var parameters tunnelParameters
	err = json.Unmarshal(res[:length], &parameters)
	if err != nil {
		return tunnelParameters{}, err
	}
	return parameters, nil
}
