package tunnel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTlvBuffer_WriteByte(t *testing.T) {
	buf := newTlvBuffer()
	buf.writeByte(typeState, 0x01)

	expected := []byte{
		byte(typeState), // type
		0x01,            // length
		0x01,            // value
	}
	assert.Equal(t, expected, buf.bytes())
}

func TestTlvBuffer_WriteData_Short(t *testing.T) {
	buf := newTlvBuffer()
	data := []byte{0x01, 0x02, 0x03}
	buf.writeData(typePublicKey, data)

	expected := []byte{
		byte(typePublicKey), // type
		0x03,                // length
		0x01, 0x02, 0x03,    // data
	}
	assert.Equal(t, expected, buf.bytes())
}

func TestTlvBuffer_WriteData_Empty(t *testing.T) {
	buf := newTlvBuffer()
	buf.writeData(typeSalt, []byte{})

	expected := []byte{
		byte(typeSalt), // type
		0x00,           // length
	}
	assert.Equal(t, expected, buf.bytes())
}

func TestTlvBuffer_WriteData_ExactlyMaxUint8(t *testing.T) {
	buf := newTlvBuffer()
	data := make([]byte, 255)
	for i := range data {
		data[i] = byte(i)
	}
	buf.writeData(typeEncryptedData, data)

	result := buf.bytes()
	assert.Equal(t, byte(typeEncryptedData), result[0])
	assert.Equal(t, byte(255), result[1])
	assert.Equal(t, data, result[2:])
}

func TestTlvBuffer_WriteData_ChunkingOver255Bytes(t *testing.T) {
	buf := newTlvBuffer()
	// Create data that is 300 bytes (255 + 45)
	data := make([]byte, 300)
	for i := range data {
		data[i] = byte(i % 256)
	}
	buf.writeData(typeEncryptedData, data)

	result := buf.bytes()

	// First chunk: type + 255 + 255 bytes
	assert.Equal(t, byte(typeEncryptedData), result[0])
	assert.Equal(t, byte(255), result[1])
	assert.Equal(t, data[:255], result[2:257])

	// Second chunk: type + 45 + 45 bytes
	assert.Equal(t, byte(typeEncryptedData), result[257])
	assert.Equal(t, byte(45), result[258])
	assert.Equal(t, data[255:], result[259:])
}

func TestTlvBuffer_WriteData_MultipleChunks(t *testing.T) {
	buf := newTlvBuffer()
	// Create data that is 600 bytes (255 + 255 + 90)
	data := make([]byte, 600)
	for i := range data {
		data[i] = byte(i % 256)
	}
	buf.writeData(typePublicKey, data)

	result := buf.bytes()

	// First chunk: 255 bytes
	assert.Equal(t, byte(typePublicKey), result[0])
	assert.Equal(t, byte(255), result[1])

	// Second chunk: 255 bytes
	assert.Equal(t, byte(typePublicKey), result[257])
	assert.Equal(t, byte(255), result[258])

	// Third chunk: 90 bytes
	assert.Equal(t, byte(typePublicKey), result[514])
	assert.Equal(t, byte(90), result[515])
}

func TestTlvBuffer_MultipleWrites(t *testing.T) {
	buf := newTlvBuffer()
	buf.writeByte(typeMethod, 0x00)
	buf.writeByte(typeState, 0x01)
	buf.writeData(typeIdentifier, []byte("test-id"))

	result := buf.bytes()

	// First TLV: method = 0x00
	assert.Equal(t, byte(typeMethod), result[0])
	assert.Equal(t, byte(1), result[1])
	assert.Equal(t, byte(0x00), result[2])

	// Second TLV: state = 0x01
	assert.Equal(t, byte(typeState), result[3])
	assert.Equal(t, byte(1), result[4])
	assert.Equal(t, byte(0x01), result[5])

	// Third TLV: identifier = "test-id"
	assert.Equal(t, byte(typeIdentifier), result[6])
	assert.Equal(t, byte(7), result[7])
	assert.Equal(t, []byte("test-id"), result[8:15])
}

func TestTlvReader_ReadCoalesced_SimpleValue(t *testing.T) {
	// Type=0x06, Length=1, Value=0x01
	data := tlvReader([]byte{0x06, 0x01, 0x01})
	result, err := data.readCoalesced(typeState)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x01}, result)
}

func TestTlvReader_ReadCoalesced_EmptyValue(t *testing.T) {
	// Type=0x06, Length=0
	data := tlvReader([]byte{0x06, 0x00})
	result, err := data.readCoalesced(typeState)
	require.NoError(t, err)
	assert.Equal(t, []byte{}, result)
}

func TestTlvReader_ReadCoalesced_TypeNotFound(t *testing.T) {
	// Type=0x06, but we're looking for 0x07
	data := tlvReader([]byte{0x06, 0x01, 0x01})
	result, err := data.readCoalesced(typeError)
	require.NoError(t, err)
	assert.Empty(t, result) // nil or empty slice both acceptable
}

func TestTlvReader_ReadCoalesced_MultipleTypes(t *testing.T) {
	// Two different types
	data := tlvReader([]byte{
		0x06, 0x01, 0x01, // state = 0x01
		0x07, 0x01, 0x02, // error = 0x02
	})

	stateResult, err := data.readCoalesced(typeState)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x01}, stateResult)

	errorResult, err := data.readCoalesced(typeError)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x02}, errorResult)
}

func TestTlvReader_ReadCoalesced_FragmentedData(t *testing.T) {
	// Same type appears multiple times (fragmented)
	data := tlvReader([]byte{
		byte(typePublicKey), 0x03, 0x01, 0x02, 0x03, // first chunk
		byte(typePublicKey), 0x02, 0x04, 0x05, // second chunk
	})

	result, err := data.readCoalesced(typePublicKey)
	require.NoError(t, err)
	// Should coalesce both chunks
	assert.Equal(t, []byte{0x01, 0x02, 0x03, 0x04, 0x05}, result)
}

func TestTlvReader_ReadCoalesced_InterleavedTypes(t *testing.T) {
	// Fragments of one type interleaved with another type
	data := tlvReader([]byte{
		byte(typePublicKey), 0x02, 0x01, 0x02, // first pk chunk
		byte(typeState), 0x01, 0xAA, // state value
		byte(typePublicKey), 0x02, 0x03, 0x04, // second pk chunk
	})

	pkResult, err := data.readCoalesced(typePublicKey)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, pkResult)

	stateResult, err := data.readCoalesced(typeState)
	require.NoError(t, err)
	assert.Equal(t, []byte{0xAA}, stateResult)
}

func TestTlvReader_ReadCoalesced_EmptyInput(t *testing.T) {
	data := tlvReader([]byte{})
	result, err := data.readCoalesced(typeState)
	require.NoError(t, err)
	assert.Empty(t, result) // nil or empty slice both acceptable
}

func TestTlvError_Error(t *testing.T) {
	tests := []struct {
		code     tlvError
		expected string
	}{
		{tlvError(0), "reserved0"},
		{tlvError(1), "unknown"},
		{tlvError(2), "authentication"},
		{tlvError(3), "backoff"},
		{tlvError(4), "unknownpeer"},
		{tlvError(5), "maxpeers"},
		{tlvError(6), "maxtries"},
		{tlvError(7), "unknown error code '7'"},
		{tlvError(100), "unknown error code '100'"},
		{tlvError(255), "unknown error code '255'"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.code.Error())
		})
	}
}

func TestTlvRoundTrip(t *testing.T) {
	// Write data using tlvBuffer, then read it back using tlvReader
	buf := newTlvBuffer()
	publicKey := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	salt := []byte{0xAA, 0xBB, 0xCC}
	state := byte(0x03)

	buf.writeData(typePublicKey, publicKey)
	buf.writeData(typeSalt, salt)
	buf.writeByte(typeState, state)

	reader := tlvReader(buf.bytes())

	readPK, err := reader.readCoalesced(typePublicKey)
	require.NoError(t, err)
	assert.Equal(t, publicKey, readPK)

	readSalt, err := reader.readCoalesced(typeSalt)
	require.NoError(t, err)
	assert.Equal(t, salt, readSalt)

	readState, err := reader.readCoalesced(typeState)
	require.NoError(t, err)
	assert.Equal(t, []byte{state}, readState)
}

func TestTlvRoundTrip_LargeData(t *testing.T) {
	// Test round-trip with data larger than 255 bytes
	buf := newTlvBuffer()
	largeData := make([]byte, 500)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	buf.writeData(typeEncryptedData, largeData)

	reader := tlvReader(buf.bytes())
	result, err := reader.readCoalesced(typeEncryptedData)
	require.NoError(t, err)
	assert.Equal(t, largeData, result)
}
