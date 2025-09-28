package rdp

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestParseTPKTHeader(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		expected    *TPKTHeader
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid TPKT header",
			data: []byte{0x03, 0x00, 0x00, 0x10}, // version=3, reserved=0, length=16
			expected: &TPKTHeader{
				Version:  0x03,
				Reserved: 0x00,
				Length:   16,
			},
			expectError: false,
		},
		{
			name: "valid TPKT header with non-zero reserved",
			data: []byte{0x03, 0x01, 0x00, 0x20}, // version=3, reserved=1, length=32
			expected: &TPKTHeader{
				Version:  0x03,
				Reserved: 0x01,
				Length:   32,
			},
			expectError: false,
		},
		{
			name:        "insufficient data",
			data:        []byte{0x03, 0x00}, // only 2 bytes
			expected:    nil,
			expectError: true,
			errorMsg:    "insufficient data for TPKT header",
		},
		{
			name:        "invalid version",
			data:        []byte{0x02, 0x00, 0x00, 0x10}, // version=2 instead of 3
			expected:    nil,
			expectError: true,
			errorMsg:    "invalid TPKT version",
		},
		{
			name:        "invalid length too small",
			data:        []byte{0x03, 0x00, 0x00, 0x02}, // length=2, less than header size
			expected:    nil,
			expectError: true,
			errorMsg:    "invalid TPKT length",
		},
		{
			name:        "empty data",
			data:        []byte{},
			expected:    nil,
			expectError: true,
			errorMsg:    "insufficient data for TPKT header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseTPKTHeader(tt.data)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if result == nil {
					t.Errorf("expected result but got nil")
					return
				}
				if result.Version != tt.expected.Version {
					t.Errorf("expected version %d, got %d", tt.expected.Version, result.Version)
				}
				if result.Reserved != tt.expected.Reserved {
					t.Errorf("expected reserved %d, got %d", tt.expected.Reserved, result.Reserved)
				}
				if result.Length != tt.expected.Length {
					t.Errorf("expected length %d, got %d", tt.expected.Length, result.Length)
				}
			}
		})
	}
}

func TestParseTPDUHeader(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		expected    *TPDUHeader
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid TPDU header with data",
			data: []byte{0x03, 0xE0, 0x01, 0x02, 0x03}, // length=3, code=0xE0, data=[1,2,3]
			expected: &TPDUHeader{
				LengthIndicator: 0x03,
				Code:            0xE0,
				Data:            []byte{0x01, 0x02, 0x03},
			},
			expectError: false,
		},
		{
			name: "valid TPDU header without data",
			data: []byte{0x00, 0xE0}, // length=0, code=0xE0
			expected: &TPDUHeader{
				LengthIndicator: 0x00,
				Code:            0xE0,
				Data:            nil,
			},
			expectError: false,
		},
		{
			name: "valid TPDU header with single byte data",
			data: []byte{0x01, 0xE0, 0x42}, // length=1, code=0xE0, data=[0x42]
			expected: &TPDUHeader{
				LengthIndicator: 0x01,
				Code:            0xE0,
				Data:            []byte{0x42},
			},
			expectError: false,
		},
		{
			name:        "insufficient data for header",
			data:        []byte{0x05}, // only 1 byte
			expected:    nil,
			expectError: true,
			errorMsg:    "insufficient data for TPDU header",
		},
		{
			name:        "insufficient data for payload",
			data:        []byte{0x05, 0xE0, 0x01, 0x02}, // length=5 but only 4 bytes total
			expected:    nil,
			expectError: true,
			errorMsg:    "insufficient data for TPDU payload",
		},
		{
			name:        "empty data",
			data:        []byte{},
			expected:    nil,
			expectError: true,
			errorMsg:    "insufficient data for TPDU header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseTPDUHeader(tt.data)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if result == nil {
					t.Errorf("expected result but got nil")
					return
				}
				if result.LengthIndicator != tt.expected.LengthIndicator {
					t.Errorf("expected length indicator %d, got %d", tt.expected.LengthIndicator, result.LengthIndicator)
				}
				if result.Code != tt.expected.Code {
					t.Errorf("expected code %d, got %d", tt.expected.Code, result.Code)
				}
				if !bytes.Equal(result.Data, tt.expected.Data) {
					t.Errorf("expected data %v, got %v", tt.expected.Data, result.Data)
				}
			}
		})
	}
}

func TestExtractCredentialsFromRDP(t *testing.T) {
	tests := []struct {
		name        string
		rdpData     []byte
		expected    string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid RDP data with mstshash",
			rdpData: createValidRDPPacket("mstshash=testuser"),
			expected: "testuser",
			expectError: false,
		},
		{
			name: "valid RDP data with msthash",
			rdpData: createValidRDPPacket("msthash=anotheruser"),
			expected: "anotheruser",
			expectError: false,
		},
		{
			name: "valid RDP data with mstshash and newline",
			rdpData: createValidRDPPacket("mstshash=user123\r\n"),
			expected: "user123",
			expectError: false,
		},
		{
			name: "valid RDP data with mstshash and null terminator",
			rdpData: createValidRDPPacket("mstshash=user456\x00"),
			expected: "user456",
			expectError: false,
		},
		{
			name: "valid RDP data with mstshash in middle of string",
			rdpData: createValidRDPPacket("prefix mstshash=user789 suffix"),
			expected: "user789 suffix",
			expectError: false,
		},
		{
			name: "valid RDP data without credentials (fallback to default)",
			rdpData: createValidRDPPacket("some other data"),
			expected: "default_user",
			expectError: false,
		},
		{
			name: "invalid TPKT header",
			rdpData: []byte{0x02, 0x00, 0x00, 0x10}, // wrong version
			expected: "",
			expectError: true,
			errorMsg: "failed to parse TPKT header",
		},
		{
			name: "incomplete TPKT packet",
			rdpData: []byte{0x03, 0x00, 0x00, 0x20, 0x01, 0x02}, // length=32 but only 6 bytes
			expected: "",
			expectError: true,
			errorMsg: "incomplete TPKT packet",
		},
		{
			name: "empty data",
			rdpData: []byte{},
			expected: "",
			expectError: true,
			errorMsg: "failed to parse TPKT header",
		},
		{
			name: "TPKT header only",
			rdpData: []byte{0x03, 0x00, 0x00, 0x04}, // only header, no payload
			expected: "",
			expectError: true,
			errorMsg: "no TPDU data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractCredentialsFromRDP(tt.rdpData)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if result != tt.expected {
					t.Errorf("expected %q, got %q", tt.expected, result)
				}
			}
		})
	}
}

func TestExtractCredentialsFromTPDUData(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected string
	}{
		{
			name:     "mstshash with newline",
			data:     []byte("mstshash=testuser\r\n"),
			expected: "testuser",
		},
		{
			name:     "mstshash with null terminator",
			data:     []byte("mstshash=testuser\x00"),
			expected: "testuser",
		},
		{
			name:     "mstshash with space",
			data:     []byte("mstshash=testuser "),
			expected: "testuser ",
		},
		{
			name:     "msthash pattern",
			data:     []byte("msthash=anotheruser\r\n"),
			expected: "anotheruser",
		},
		{
			name:     "mstshash in middle of string",
			data:     []byte("prefix mstshash=middleuser suffix"),
			expected: "middleuser suffix",
		},
		{
			name:     "mstshash with special characters",
			data:     []byte("mstshash=user@domain.com\r\n"),
			expected: "user@domain.com",
		},
		{
			name:     "mstshash with underscore",
			data:     []byte("mstshash=user_name\r\n"),
			expected: "user_name",
		},
		{
			name:     "mstshash with numbers",
			data:     []byte("mstshash=user123\r\n"),
			expected: "user123",
		},
		{
			name:     "no credentials found",
			data:     []byte("some other data"),
			expected: "default_user",
		},
		{
			name:     "empty data",
			data:     []byte(""),
			expected: "default_user",
		},
		{
			name:     "mstshash with empty value",
			data:     []byte("mstshash=\r\n"),
			expected: "default_user",
		},
		{
			name:     "mstshash= but no value",
			data:     []byte("mstshash="),
			expected: "default_user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractCredentialsFromTPDUData(tt.data)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestReadFirstRDPPacket(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		expected    []byte
		expectError bool
		errorMsg    string
	}{
		{
			name:     "valid RDP packet",
			data:     createValidRDPPacket("test data"),
			expected: createValidRDPPacket("test data"),
			expectError: false,
		},
		{
			name:     "valid RDP packet with longer data",
			data:     createValidRDPPacket("mstshash=testuser\r\nadditional data"),
			expected: createValidRDPPacket("mstshash=testuser\r\nadditional data"),
			expectError: false,
		},
		{
			name:     "empty packet",
			data:     []byte{0x03, 0x00, 0x00, 0x04}, // header only
			expected: []byte{0x03, 0x00, 0x00, 0x04},
			expectError: false,
		},
		{
			name:        "invalid TPKT version",
			data:        []byte{0x02, 0x00, 0x00, 0x10, 0x01, 0x02, 0x03, 0x04},
			expected:    nil,
			expectError: true,
			errorMsg:    "failed to parse TPKT header",
		},
		{
			name:        "invalid TPKT length",
			data:        []byte{0x03, 0x00, 0x00, 0x02}, // length=2, less than header size
			expected:    nil,
			expectError: true,
			errorMsg:    "invalid TPKT length",
		},
		{
			name:        "incomplete header",
			data:        []byte{0x03, 0x00}, // incomplete header
			expected:    nil,
			expectError: true,
			errorMsg:    "failed to read TPKT header",
		},
		{
			name:        "incomplete payload",
			data:        []byte{0x03, 0x00, 0x00, 0x10, 0x01, 0x02}, // length=16 but only 6 bytes
			expected:    nil,
			expectError: true,
			errorMsg:    "failed to read TPKT payload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bytes.NewReader(tt.data)
			result, err := ReadFirstRDPPacket(reader)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if !bytes.Equal(result, tt.expected) {
					t.Errorf("expected %v, got %v", tt.expected, result)
				}
			}
		})
	}
}

func TestReadFirstRDPPacketWithIncompleteRead(t *testing.T) {
	// Test with a reader that returns partial data
	partialData := []byte{0x03, 0x00, 0x00, 0x10, 0x01, 0x02} // length=16 but only 6 bytes
	reader := bytes.NewReader(partialData)
	
	_, err := ReadFirstRDPPacket(reader)
	if err == nil {
		t.Errorf("expected error for incomplete read but got none")
		return
	}
	if !strings.Contains(err.Error(), "failed to read TPKT payload") {
		t.Errorf("expected error message to contain 'failed to read TPKT payload', got %q", err.Error())
	}
}

func TestReadFirstRDPPacketWithEmptyReader(t *testing.T) {
	// Test with an empty reader
	reader := bytes.NewReader([]byte{})
	
	_, err := ReadFirstRDPPacket(reader)
	if err == nil {
		t.Errorf("expected error for empty reader but got none")
		return
	}
	if !strings.Contains(err.Error(), "failed to read TPKT header") {
		t.Errorf("expected error message to contain 'failed to read TPKT header', got %q", err.Error())
	}
}

// Helper function to create a valid RDP packet for testing
func createValidRDPPacket(payload string) []byte {
	// Create TPKT header
	tpktHeader := make([]byte, TPKTHeaderSize)
	tpktHeader[0] = TPKTVersion
	tpktHeader[1] = 0x00 // reserved
	
	// Create TPDU header
	tpduHeader := make([]byte, 2)
	tpduHeader[0] = uint8(len(payload)) // length indicator
	tpduHeader[1] = TPDUConnectionRequest // code
	
	// Calculate total length
	totalLength := TPKTHeaderSize + len(tpduHeader) + len(payload)
	binary.BigEndian.PutUint16(tpktHeader[2:4], uint16(totalLength))
	
	// Combine all parts
	packet := make([]byte, totalLength)
	copy(packet[:TPKTHeaderSize], tpktHeader)
	copy(packet[TPKTHeaderSize:TPKTHeaderSize+2], tpduHeader)
	copy(packet[TPKTHeaderSize+2:], []byte(payload))
	
	return packet
}

// Test constants
func TestConstants(t *testing.T) {
	if TPKTVersion != 0x03 {
		t.Errorf("expected TPKTVersion to be 0x03, got 0x%02x", TPKTVersion)
	}
	if TPKTHeaderSize != 4 {
		t.Errorf("expected TPKTHeaderSize to be 4, got %d", TPKTHeaderSize)
	}
	if TPDUConnectionRequest != 0xE0 {
		t.Errorf("expected TPDUConnectionRequest to be 0xE0, got 0x%02x", TPDUConnectionRequest)
	}
}

// Test TPKTHeader struct
func TestTPKTHeaderStruct(t *testing.T) {
	header := &TPKTHeader{
		Version:  0x03,
		Reserved: 0x00,
		Length:   16,
	}
	
	if header.Version != 0x03 {
		t.Errorf("expected Version to be 0x03, got 0x%02x", header.Version)
	}
	if header.Reserved != 0x00 {
		t.Errorf("expected Reserved to be 0x00, got 0x%02x", header.Reserved)
	}
	if header.Length != 16 {
		t.Errorf("expected Length to be 16, got %d", header.Length)
	}
}

// Test TPDUHeader struct
func TestTPDUHeaderStruct(t *testing.T) {
	header := &TPDUHeader{
		LengthIndicator: 5,
		Code:            0xE0,
		Data:            []byte{0x01, 0x02, 0x03},
	}
	
	if header.LengthIndicator != 5 {
		t.Errorf("expected LengthIndicator to be 5, got %d", header.LengthIndicator)
	}
	if header.Code != 0xE0 {
		t.Errorf("expected Code to be 0xE0, got 0x%02x", header.Code)
	}
	if !bytes.Equal(header.Data, []byte{0x01, 0x02, 0x03}) {
		t.Errorf("expected Data to be [0x01, 0x02, 0x03], got %v", header.Data)
	}
}
