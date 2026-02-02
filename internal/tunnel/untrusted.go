package tunnel

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"fmt"

	"io"

	"github.com/dmdmdm-nz/tunneld/internal/tunnel/opack"
	"github.com/dmdmdm-nz/tunneld/internal/tunnel/xpc"

	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/hkdf"
)

// UntrustedTunnelServiceName is the service name that is described in the Remote Service Discovery of the
// ethernet interface of the device (not the tunnel interface)
const UntrustedTunnelServiceName = "com.apple.internal.dt.coredevice.untrusted.tunnelservice"

type PairingResult struct {
	SharedSecret []byte
	NeedsPairing bool
}

func newTunnelServiceWithXpc(xpcConn *xpc.Connection, c io.Closer, pairRecords *PairRecordManager) *tunnelService {
	return &tunnelService{
		xpcConn:        xpcConn,
		c:              c,
		controlChannel: newControlChannelReadWriter(xpcConn),
		pairRecords:    pairRecords,
	}
}

// deviceKeyData holds the device's SRP public key and salt received during pairing
type deviceKeyData struct {
	publicKey []byte
	salt      []byte
}

type tunnelService struct {
	xpcConn *xpc.Connection
	c       io.Closer

	controlChannel *controlChannelReadWriter
	cipher         *cipherStream

	pairRecords *PairRecordManager

	// pendingDeviceKey holds device key data if it was received in the setupManualPairing response
	pendingDeviceKey *deviceKeyData
}

func (t *tunnelService) Close() error {
	return t.c.Close()
}

// VerifyPairing verifies that there is already an active pairing with the credentials stored in PairRecordManager
func (t *tunnelService) VerifyPairing(ctx context.Context, udid string) (PairingResult, error) {
	err := t.controlChannel.writeRequest(map[string]interface{}{
		"handshake": map[string]interface{}{
			"_0": map[string]interface{}{
				"hostOptions": map[string]interface{}{
					"attemptPairVerify": true,
				},
				"wireProtocolVersion": int64(19),
			},
		},
	})

	if err != nil {
		return PairingResult{}, fmt.Errorf("ManualPair: failed to send 'attemptPairVerify' request: %w", err)
	}
	// ignore the response for now
	_, err = t.controlChannel.read()
	if err != nil {
		return PairingResult{}, fmt.Errorf("ManualPair: failed to read 'attemptPairVerify' response: %w", err)
	}

	sharedSecret, err := t.verifyPair()
	if err == nil {
		return PairingResult{SharedSecret: sharedSecret}, nil
	}

	return PairingResult{SharedSecret: sharedSecret, NeedsPairing: true}, nil
}

// ManualPair triggers a device pairing that requires the user to press the 'Trust' button on the device that appears
// when this operation is triggered
// If there is already an active pairing with the credentials stored in PairRecordManager this call does not trigger
// anything on the device and returns with an error
func (t *tunnelService) ManualPair(ctx context.Context, udid string, sharedSecret []byte) (PairingResult, error) {
	err := t.setupManualPairing()
	if err != nil {
		return PairingResult{}, fmt.Errorf("ManualPair: failed to initiate manual pairing: %w", err)
	}

	sessionKey, err := t.setupSessionKey(ctx)
	if err != nil {
		return PairingResult{}, fmt.Errorf("ManualPair: failed to setup SRP session key: %w", err)
	}

	err = t.exchangeDeviceInfo(sessionKey)
	if err != nil {
		return PairingResult{}, fmt.Errorf("ManualPair: failed to exchange device info: %w", err)
	}

	err = t.setupCiphers(sessionKey)
	if err != nil {
		return PairingResult{}, fmt.Errorf("ManualPair: failed to setup session ciphers: %w", err)
	}

	_, err = t.createUnlockKey()
	if err != nil {
		return PairingResult{}, fmt.Errorf("ManualPair: failed to create unlock key: %w", err)
	}

	return PairingResult{SharedSecret: sharedSecret}, nil
}

func (t *tunnelService) createTunnelListener(sharedSecret []byte) (tunnelListener, error) {
	err := t.cipher.write(map[string]interface{}{
		"request": map[string]interface{}{
			"_0": map[string]interface{}{
				"createListener": map[string]interface{}{
					"key":                   base64.StdEncoding.EncodeToString(sharedSecret),
					"transportProtocolType": "tcp",
				},
			},
		},
	})
	if err != nil {
		return tunnelListener{}, err
	}

	var listenerRes map[string]interface{}
	err = t.cipher.read(&listenerRes)
	if err != nil {
		return tunnelListener{}, err
	}

	createListener, err := getChildMap(listenerRes, "response", "_1", "createListener")
	if err != nil {
		return tunnelListener{}, err
	}
	port := createListener["port"].(float64)
	devPublicKeyRaw, found := createListener["devicePublicKey"]
	if !found {
		return tunnelListener{}, fmt.Errorf("no public key found")
	}
	devPublicKey, isString := devPublicKeyRaw.(string)
	if !isString {
		return tunnelListener{}, fmt.Errorf("public key is not a string")
	}
	devPK, err := base64.StdEncoding.DecodeString(devPublicKey)
	if err != nil {
		return tunnelListener{}, err
	}
	publicKey, err := x509.ParsePKIXPublicKey(devPK)
	if err != nil {
		return tunnelListener{}, err
	}
	return tunnelListener{
		PrivateKey:      nil,
		DevicePublicKey: publicKey,
		TunnelPort:      uint64(port),
	}, nil
}

func (t *tunnelService) setupCiphers(sessionKey []byte) error {
	clientKey := make([]byte, 32)
	_, err := hkdf.New(sha512.New, sessionKey, nil, []byte("ClientEncrypt-main")).Read(clientKey)
	if err != nil {
		return err
	}
	serverKey := make([]byte, 32)
	_, err = hkdf.New(sha512.New, sessionKey, nil, []byte("ServerEncrypt-main")).Read(serverKey)
	if err != nil {
		return err
	}
	server, err := chacha20poly1305.New(serverKey)
	if err != nil {
		return err
	}
	client, err := chacha20poly1305.New(clientKey)
	if err != nil {
		return err
	}

	t.cipher = newCipherStream(t.controlChannel, client, server)

	return nil
}

func (t *tunnelService) setupManualPairing() error {
	buf := newTlvBuffer()
	buf.writeByte(typeMethod, 0x00)
	buf.writeByte(typeState, pairStateStartRequest)

	event := pairingData{
		data:            buf.bytes(),
		kind:            "setupManualPairing",
		startNewSession: true,
	}

	err := t.controlChannel.writeEvent(&event)
	if err != nil {
		return err
	}

	// Read the response - this is an acknowledgment, not a pairingData event
	_, err = t.controlChannel.read()
	if err != nil {
		return fmt.Errorf("setupManualPairing: failed to read response: %w", err)
	}

	return nil
}

func (t *tunnelService) readDeviceKey(ctx context.Context) (publicKey []byte, salt []byte, err error) {
	// Check if we already have the device key data from setupManualPairing response
	if t.pendingDeviceKey != nil {
		publicKey = t.pendingDeviceKey.publicKey
		salt = t.pendingDeviceKey.salt
		t.pendingDeviceKey = nil // Clear after use
		return
	}

	// Otherwise, wait for the device to send the key data (after user presses Trust)
	var pairingData pairingData
	done := make(chan error, 1)

	go func() {
		done <- t.controlChannel.readEvent(&pairingData)
	}()

	select {
	case <-ctx.Done():
		err = ctx.Err()
		return
	case err = <-done:
		if err != nil {
			return
		}
	}

	// Check if the response contains an error
	errRes, parseErr := tlvReader(pairingData.data).readCoalesced(typeError)
	if parseErr == nil && len(errRes) > 0 {
		err = fmt.Errorf("device returned error: %w", tlvError(errRes[0]))
		return
	}

	publicKey, err = tlvReader(pairingData.data).readCoalesced(typePublicKey)
	if err != nil {
		return
	}
	salt, err = tlvReader(pairingData.data).readCoalesced(typeSalt)
	if err != nil {
		return
	}
	return
}

func (t *tunnelService) createUnlockKey() ([]byte, error) {
	err := t.cipher.write(map[string]interface{}{
		"request": map[string]interface{}{
			"_0": map[string]interface{}{
				"createRemoteUnlockKey": map[string]interface{}{},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	var res map[string]interface{}
	err = t.cipher.read(&res)
	if err != nil {
		return nil, err
	}

	return nil, err
}

func (t *tunnelService) verifyPair() ([]byte, error) {
	key, _ := ecdh.X25519().GenerateKey(rand.Reader)
	tlv := newTlvBuffer()
	tlv.writeByte(typeState, pairStateStartRequest)
	tlv.writeData(typePublicKey, key.PublicKey().Bytes())

	event := pairingData{
		data:            tlv.bytes(),
		kind:            "verifyManualPairing",
		startNewSession: true,
	}

	selfId := t.pairRecords.selfId

	err := t.controlChannel.writeEvent(&event)
	if err != nil {
		return nil, err
	}

	var devP pairingData
	err = t.controlChannel.readEvent(&devP)
	if err != nil {
		return nil, err
	}

	devicePublicKeyBytes, err := tlvReader(devP.data).readCoalesced(typePublicKey)
	if err != nil {
		return nil, err
	}

	if devicePublicKeyBytes == nil {
		_ = t.controlChannel.writeEvent(pairVerifyFailed{})
		return nil, fmt.Errorf("verifyPair: did not get public key from device. Can not verify pairing")
	}
	devicePublicKey, err := ecdh.X25519().NewPublicKey(devicePublicKeyBytes)
	if err != nil {
		return nil, err
	}

	sharedSecret, err := key.ECDH(devicePublicKey)
	if err != nil {
		return nil, err
	}

	derived := make([]byte, 32)
	_, err = hkdf.New(sha512.New, sharedSecret, []byte("Pair-Verify-Encrypt-Salt"), []byte("Pair-Verify-Encrypt-Info")).Read(derived)
	if err != nil {
		return nil, err
	}

	ci, err := chacha20poly1305.New(derived)
	if err != nil {
		return nil, err
	}

	signBuf := bytes.NewBuffer(nil)
	// Write on bytes.Buffer never returns an error
	_, _ = signBuf.Write(key.PublicKey().Bytes())
	_, _ = signBuf.Write([]byte(selfId.Identifier))
	_, _ = signBuf.Write(devicePublicKeyBytes)

	signature := ed25519.Sign(selfId.privateKey(), signBuf.Bytes())

	cTlv := newTlvBuffer()
	cTlv.writeData(typeSignature, signature)
	cTlv.writeData(typeIdentifier, []byte(selfId.Identifier))

	nonce := make([]byte, 12)
	copy(nonce[4:], "PV-Msg03")
	encrypted := ci.Seal(nil, nonce, cTlv.bytes(), nil)

	tlvOut := newTlvBuffer()
	tlvOut.writeByte(typeState, pairStateVerifyRequest)
	tlvOut.writeData(typeEncryptedData, encrypted)

	pd := pairingData{
		data:            tlvOut.bytes(),
		kind:            "verifyManualPairing",
		startNewSession: false,
	}

	err = t.controlChannel.writeEvent(&pd)
	if err != nil {
		return nil, err
	}

	var responseEvent pairingData
	err = t.controlChannel.readEvent(&responseEvent)
	if err != nil {
		return nil, err
	}

	errRes, err := tlvReader(responseEvent.data).readCoalesced(typeError)
	if err != nil {
		return nil, err
	}
	if len(errRes) > 0 {
		log.Debug("Send pair verify failed event")
		err := t.controlChannel.writeEvent(pairVerifyFailed{})
		if err != nil {
			return nil, err
		}
		return nil, tlvError(errRes[0])
	}

	err = t.setupCiphers(sharedSecret)
	if err != nil {
		return nil, err
	}

	return sharedSecret, nil
}

type tunnelListener struct {
	PrivateKey      *rsa.PrivateKey
	DevicePublicKey interface{}
	TunnelPort      uint64
}

type tunnelParameters struct {
	ServerAddress    string
	ServerRSDPort    uint64
	ClientParameters struct {
		Address string
		Netmask string
		Mtu     uint64
	}
}

func (t *tunnelService) setupSessionKey(ctx context.Context) ([]byte, error) {
	devicePublicKey, deviceSalt, err := t.readDeviceKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("setupSessionKey: failed to read device public key and salt value: %w", err)
	}

	srp, err := newSrpInfo(deviceSalt, devicePublicKey)
	if err != nil {
		return nil, fmt.Errorf("setupSessionKey: failed to setup SRP: %w", err)
	}

	proofTlv := newTlvBuffer()
	proofTlv.writeByte(typeState, pairStateVerifyRequest)
	proofTlv.writeData(typePublicKey, srp.ClientPublic)
	proofTlv.writeData(typeProof, srp.ClientProof)

	err = t.controlChannel.writeEvent(&pairingData{
		data: proofTlv.bytes(),
		kind: "setupManualPairing",
	})
	if err != nil {
		return nil, fmt.Errorf("setupSessionKey: failed to send SRP proof: %w", err)
	}

	var proofPairingData pairingData
	err = t.controlChannel.readEvent(&proofPairingData)
	if err != nil {
		return nil, fmt.Errorf("setupSessionKey: failed to read device SRP proof: %w", err)
	}

	serverProof, err := tlvReader(proofPairingData.data).readCoalesced(typeProof)
	if err != nil {
		return nil, fmt.Errorf("setupSessionKey: failed to parse device proof: %w", err)
	}
	verified := srp.verifyServerProof(serverProof)
	if !verified {
		return nil, fmt.Errorf("setupSessionKey: could not verify server proof")
	}
	return srp.SessionKey, nil
}

func (t *tunnelService) exchangeDeviceInfo(sessionKey []byte) error {
	hkdfPairSetup := hkdf.New(sha512.New, sessionKey, []byte("Pair-Setup-Controller-Sign-Salt"), []byte("Pair-Setup-Controller-Sign-Info"))
	buf := bytes.NewBuffer(nil)
	// Write on bytes.Buffer never returns an error
	_, _ = io.CopyN(buf, hkdfPairSetup, 32)
	_, _ = buf.WriteString(t.pairRecords.selfId.Identifier)
	_, _ = buf.Write(t.pairRecords.selfId.publicKey())

	signature := ed25519.Sign(t.pairRecords.selfId.privateKey(), buf.Bytes())

	// this represents the device info of this host that is stored on the device on a successful pairing.
	// The only relevant field is 'accountID' as it's used earlier in the pairing process already.
	// Everything else can be random data and is not needed later in any communication.
	deviceInfo, err := opack.Encode(map[string]interface{}{
		"accountID":                   t.pairRecords.selfId.Identifier,
		"altIRK":                      []byte{0x5e, 0xca, 0x81, 0x91, 0x92, 0x02, 0x82, 0x00, 0x11, 0x22, 0x33, 0x44, 0xbb, 0xf2, 0x4a, 0xc8},
		"btAddr":                      "FF:DD:99:66:BB:AA",
		"mac":                         []byte{0xff, 0x44, 0x88, 0x66, 0x33, 0x99},
		"model":                       "go-ios",
		"name":                        "host-name",
		"remotepairing_serial_number": "remote-serial",
	})
	if err != nil {
		return err
	}

	deviceInfoTlv := newTlvBuffer()
	deviceInfoTlv.writeData(typeSignature, signature)
	deviceInfoTlv.writeData(typePublicKey, t.pairRecords.selfId.publicKey())
	deviceInfoTlv.writeData(typeIdentifier, []byte(t.pairRecords.selfId.Identifier))
	deviceInfoTlv.writeData(typeInfo, deviceInfo)

	sessionKeyBuf := bytes.NewBuffer(nil)
	_, err = io.CopyN(sessionKeyBuf, hkdf.New(sha512.New, sessionKey, []byte("Pair-Setup-Encrypt-Salt"), []byte("Pair-Setup-Encrypt-Info")), 32)
	if err != nil {
		return err
	}
	setupKey := sessionKeyBuf.Bytes()

	cipher, err := chacha20poly1305.New(setupKey)
	if err != nil {
		return err
	}

	nonce := make([]byte, cipher.NonceSize())
	copy(nonce[4:], "PS-Msg05")
	x := cipher.Seal(nil, nonce, deviceInfoTlv.bytes(), nil)

	encryptedTlv := newTlvBuffer()
	encryptedTlv.writeByte(typeState, 0x05)
	encryptedTlv.writeData(typeEncryptedData, x)

	err = t.controlChannel.writeEvent(&pairingData{
		data:        encryptedTlv.bytes(),
		kind:        "setupManualPairing",
		sendingHost: "SL-1876",
	})
	if err != nil {
		return err
	}

	var encRes pairingData
	err = t.controlChannel.readEvent(&encRes)
	if err != nil {
		return err
	}

	encrData, err := tlvReader(encRes.data).readCoalesced(typeEncryptedData)
	if err != nil {
		return err
	}
	copy(nonce[4:], "PS-Msg06")
	// the device info response from the device is not needed. we just make sure that there's no error decrypting it
	// TODO: decode the opack encoded data and persist it using the PairRecordManager.StoreDeviceInfo method
	_, err = cipher.Open(nil, nonce, encrData, nil)
	if err != nil {
		return err
	}
	return nil
}
