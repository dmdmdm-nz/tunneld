package tls

import (
	"crypto/x509"
	"errors"
)

func init() {
	RegisterCipherSuites(pskCipherSuites...)
}

// The list of supported PSK cipher suites
var pskCipherSuites = []*CipherSuite{
	{TLS_PSK_WITH_AES_128_GCM_SHA256, 16, 0, 4, pskKA, SuiteNoCerts | SuiteTLS12, nil, nil, aeadAESGCM},
}

// A list of the possible PSK cipher suite ids.
// Note that not all of them are supported.
const (
	TLS_PSK_WITH_AES_128_GCM_SHA256 uint16 = 0x00A8
)

// Configuration for PSK cipher-suite. The client needs to provide a GetIdentity and GetKey functions to retrieve client id and pre-shared-key
type PSKConfig struct {
	// client-only - returns the client identity
	GetIdentity func() string

	// for server - returns the key associated to a client identity
	// for client - returns the key for this client
	GetKey func(identity string) ([]byte, error)
}

func pskKA(version uint16) KeyAgreement {
	return pskKeyAgreement{}
}

// pskKeyAgreement implements the standard PSK TLS key agreement
type pskKeyAgreement struct {
}

func (ka pskKeyAgreement) GenerateServerKeyExchange(config *Config, cert *Certificate, clientHello *ClientHelloMsg, hello *ServerHelloMsg) (*ServerKeyExchangeMsg, error) {
	// no server key exchange
	return nil, nil
}

func (ka pskKeyAgreement) ProcessClientKeyExchange(config *Config, cert *Certificate, ckx *ClientKeyExchangeMsg, version uint16) ([]byte, error) {

	pskConfig, ok := config.Extra.(PSKConfig)
	if !ok {
		return nil, errors.New("bad Config - Extra not of type PSKConfig")
	}

	if pskConfig.GetKey == nil {
		return nil, errors.New("bad Config - GetKey required for PSK")
	}

	if len(ckx.Ciphertext) < 2 {
		return nil, errors.New("bad ClientKeyExchange")
	}

	ciphertext := ckx.Ciphertext
	if version != VersionSSL30 {
		ciphertextLen := int(ckx.Ciphertext[0])<<8 | int(ckx.Ciphertext[1])
		if ciphertextLen != len(ckx.Ciphertext)-2 {
			return nil, errors.New("bad ClientKeyExchange")
		}
		ciphertext = ckx.Ciphertext[2:]
	}

	// ciphertext is actually the pskIdentity here
	psk, err := pskConfig.GetKey(string(ciphertext))
	if err != nil {
		return nil, err
	}

	lenpsk := len(psk)

	preMasterSecret := make([]byte, 2*lenpsk+4)
	preMasterSecret[0] = byte(lenpsk >> 8)
	preMasterSecret[1] = byte(lenpsk)
	preMasterSecret[lenpsk+2] = preMasterSecret[0]
	preMasterSecret[lenpsk+3] = preMasterSecret[1]
	copy(preMasterSecret[lenpsk+4:], psk)

	return preMasterSecret, nil
}

func (ka pskKeyAgreement) ProcessServerKeyExchange(config *Config, clientHello *ClientHelloMsg, serverHello *ServerHelloMsg, cert *x509.Certificate, skx *ServerKeyExchangeMsg) error {
	return errors.New("unexpected ServerKeyExchange")
}

func (ka pskKeyAgreement) GenerateClientKeyExchange(config *Config, clientHello *ClientHelloMsg, cert *x509.Certificate) ([]byte, *ClientKeyExchangeMsg, error) {

	pskConfig, ok := config.Extra.(PSKConfig)
	if !ok {
		return nil, nil, errors.New("bad Config - Extra not of type PSKConfig")
	}

	if pskConfig.GetIdentity == nil {
		return nil, nil, errors.New("bad PSKConfig - GetIdentity required for PSK")
	}
	if pskConfig.GetKey == nil {
		return nil, nil, errors.New("bad PSKConfig - GetKey required for PSK")
	}

	pskIdentity := pskConfig.GetIdentity()
	key, err := pskConfig.GetKey(pskIdentity)
	if err != nil {
		return nil, nil, err
	}

	psk := []byte(key)
	lenpsk := len(psk)

	preMasterSecret := make([]byte, 2*lenpsk+4)
	preMasterSecret[0] = byte(lenpsk >> 8)
	preMasterSecret[1] = byte(lenpsk)
	preMasterSecret[lenpsk+2] = preMasterSecret[0]
	preMasterSecret[lenpsk+3] = preMasterSecret[1]
	copy(preMasterSecret[lenpsk+4:], psk)

	bIdentity := []byte(pskIdentity)
	lenpski := len(bIdentity)

	ckx := new(ClientKeyExchangeMsg)
	ckx.Ciphertext = make([]byte, lenpski+2)
	ckx.Ciphertext[0] = byte(lenpski >> 8)
	ckx.Ciphertext[1] = byte(lenpski)
	copy(ckx.Ciphertext[2:], bIdentity)

	return preMasterSecret, ckx, nil
}
