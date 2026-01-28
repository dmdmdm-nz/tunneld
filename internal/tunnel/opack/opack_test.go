package opack

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateIdentifierWithLength(t *testing.T) {
	tests := []struct {
		name      string
		typeCode  byte
		length    int
		expected  []byte
		expectErr bool
	}{
		// Short lengths (≤0xF) - single byte with type|length
		{"length 0", 0x40, 0, []byte{0x40}, false},
		{"length 1", 0x40, 1, []byte{0x41}, false},
		{"length 15", 0x40, 15, []byte{0x4F}, false},

		// Medium lengths (0x10 to 0x1F) - type increment
		{"length 16", 0x40, 16, []byte{0x50}, false},
		{"length 17", 0x40, 17, []byte{0x51}, false},
		{"length 31", 0x40, 31, []byte{0x5F}, false},

		// Long lengths (0x20 to 0xFF) - two bytes
		{"length 32", 0x40, 32, []byte{0x61, 0x20}, false},
		{"length 100", 0x40, 100, []byte{0x61, 0x64}, false},
		{"length 255", 0x40, 255, []byte{0x61, 0xFF}, false},

		// Error case - too long
		{"length 256 error", 0x40, 256, nil, true},
		{"length 1000 error", 0x40, 1000, nil, true},

		// Different type codes
		{"data type length 0", 0x70, 0, []byte{0x70}, false},
		{"data type length 15", 0x70, 15, []byte{0x7F}, false},
		{"data type length 16", 0x70, 16, []byte{0x80}, false},
		{"data type length 32", 0x70, 32, []byte{0x91, 0x20}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := createIdentifierWithLength(tc.typeCode, tc.length)
			if tc.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "string too long")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestEncodeString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []byte
	}{
		{"empty string", "", []byte{0x40}},
		{"single char", "a", []byte{0x41, 'a'}},
		{"short string", "hello", []byte{0x45, 'h', 'e', 'l', 'l', 'o'}},
		{"15 char string", "123456789012345", append([]byte{0x4F}, []byte("123456789012345")...)},
		{"16 char string", "1234567890123456", append([]byte{0x50}, []byte("1234567890123456")...)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Encode(map[string]interface{}{"k": tc.input})
			require.NoError(t, err)

			// Result contains: dict header + "k" key encoding + string encoding
			// Extract just the string encoding part for comparison
			// Dict header: 0xE1 (1 entry)
			// Key "k": 0x41 'k'
			// Then our string encoding follows
			assert.Equal(t, byte(0xE1), result[0])        // dict with 1 entry
			assert.Equal(t, []byte{0x41, 'k'}, result[1:3]) // key "k"
			assert.Equal(t, tc.expected, result[3:])        // string value
		})
	}
}

func TestEncodeData(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{"empty data", []byte{}, []byte{0x70}},
		{"single byte", []byte{0xAB}, []byte{0x71, 0xAB}},
		{"four bytes", []byte{0x01, 0x02, 0x03, 0x04}, []byte{0x74, 0x01, 0x02, 0x03, 0x04}},
		{"15 bytes", make([]byte, 15), append([]byte{0x7F}, make([]byte, 15)...)},
		{"16 bytes", make([]byte, 16), append([]byte{0x80}, make([]byte, 16)...)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Encode(map[string]interface{}{"k": tc.input})
			require.NoError(t, err)

			// Extract just the data encoding part
			assert.Equal(t, byte(0xE1), result[0])          // dict with 1 entry
			assert.Equal(t, []byte{0x41, 'k'}, result[1:3]) // key "k"
			assert.Equal(t, tc.expected, result[3:])        // data value
		})
	}
}

func TestEncode_EmptyDict(t *testing.T) {
	result, err := Encode(map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, []byte{0xE0}, result)
}

func TestEncode_SingleStringEntry(t *testing.T) {
	result, err := Encode(map[string]interface{}{
		"a": "b",
	})
	require.NoError(t, err)

	// 0xE1 = dict with 1 entry
	// 0x41 'a' = key "a"
	// 0x41 'b' = value "b"
	expected := []byte{0xE1, 0x41, 'a', 0x41, 'b'}
	assert.Equal(t, expected, result)
}

func TestEncode_SingleDataEntry(t *testing.T) {
	result, err := Encode(map[string]interface{}{
		"d": []byte{0x01, 0x02},
	})
	require.NoError(t, err)

	// 0xE1 = dict with 1 entry
	// 0x41 'd' = key "d"
	// 0x72 0x01 0x02 = data of length 2
	expected := []byte{0xE1, 0x41, 'd', 0x72, 0x01, 0x02}
	assert.Equal(t, expected, result)
}

func TestEncode_MultipleEntries(t *testing.T) {
	result, err := Encode(map[string]interface{}{
		"a": "1",
		"b": "2",
	})
	require.NoError(t, err)

	// 0xE2 = dict with 2 entries
	assert.Equal(t, byte(0xE2), result[0])
	// The rest contains the two entries (order may vary due to map iteration)
	// 1 header + 2 * (2 bytes key encoding + 2 bytes value encoding) = 1 + 2*4 = 9 bytes
	assert.Len(t, result, 9)
}

func TestEncode_TooManyEntries(t *testing.T) {
	// Create a map with 16 entries (exceeds 0xF limit)
	m := make(map[string]interface{})
	for i := 0; i < 16; i++ {
		m[string(rune('a'+i))] = "x"
	}

	_, err := Encode(m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds max size")
}

func TestEncode_UnsupportedType(t *testing.T) {
	_, err := Encode(map[string]interface{}{
		"num": 42, // int is not supported, only string and []byte
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "can't encode type")
}

func TestEncode_LongerString(t *testing.T) {
	// Test with a 32-character string (needs two-byte length encoding)
	longStr := "12345678901234567890123456789012"
	result, err := Encode(map[string]interface{}{
		"k": longStr,
	})
	require.NoError(t, err)

	// 0xE1 = dict with 1 entry
	// 0x41 'k' = key "k"
	// 0x61 0x20 + 32 chars = string with length 32
	assert.Equal(t, byte(0xE1), result[0])
	assert.Equal(t, []byte{0x41, 'k'}, result[1:3])
	assert.Equal(t, byte(0x61), result[3]) // type + length indicator
	assert.Equal(t, byte(0x20), result[4]) // length = 32
	assert.Equal(t, longStr, string(result[5:]))
}
