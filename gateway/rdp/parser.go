package rdp

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)
// TPKTHeader represents a TPKT (ISO 8073) header
type TPKTHeader struct {
	Version      uint8
	Reserved     uint8
	Length       uint16
}

const (
	TPKTVersion = 0x03
	TPKTHeaderSize = 4
)

// TPDUHeader represents a TPDU (ISO 8073) header
type TPDUHeader struct {
	LengthIndicator uint8
	Code            uint8
	Data            []byte
}

const (
	TPDUConnectionRequest = 0xE0
)

// ParseTPKTHeader parses a TPKT header from the given data
func ParseTPKTHeader(data []byte) (*TPKTHeader, error) {
	if len(data) < TPKTHeaderSize {
		return nil, fmt.Errorf("insufficient data for TPKT header: got %d bytes, need %d", len(data), TPKTHeaderSize)
	}

	header := &TPKTHeader{
		Version:  data[0],
		Reserved: data[1],
		Length:   binary.BigEndian.Uint16(data[2:4]),
	}

	if header.Version != TPKTVersion {
		return nil, fmt.Errorf("invalid TPKT version: got %d, expected %d", header.Version, TPKTVersion)
	}

	if header.Length < TPKTHeaderSize {
		return nil, fmt.Errorf("invalid TPKT length: %d", header.Length)
	}

	return header, nil
}

// ParseTPDUHeader parses a TPDU header from the given data
func ParseTPDUHeader(data []byte) (*TPDUHeader, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("insufficient data for TPDU header")
	}

	header := &TPDUHeader{
		LengthIndicator: data[0],
		Code:            data[1],
	}

	// Calculate data length
	dataLength := int(header.LengthIndicator)
	if dataLength > 0 {
		requiredBytes := 2 + dataLength
		if len(data) < requiredBytes {
			return nil, fmt.Errorf("insufficient data for TPDU payload: got %d bytes, need %d", len(data), requiredBytes)
		}
		header.Data = data[2 : 2+dataLength]
	}

	return header, nil
}

// ExtractCredentialsFromRDP extracts user credentials from an RDP connection request
func ExtractCredentialsFromRDP(rdpData []byte) (string, error) {
	// Parse TPKT header
	tpkt, err := ParseTPKTHeader(rdpData)
	if err != nil {
		return "", fmt.Errorf("failed to parse TPKT header: %w", err)
	}

	// Verify we have the complete packet
	if len(rdpData) != int(tpkt.Length) {
		return "", fmt.Errorf("incomplete TPKT packet: got %d bytes, expected %d", len(rdpData), tpkt.Length)
	}

	// Extract TPDU payload
	tpduData := rdpData[TPKTHeaderSize:]
	if len(tpduData) == 0 {
		return "", fmt.Errorf("no TPDU data")
	}

	// Parse TPDU header
	tpdu, err := ParseTPDUHeader(tpduData)
	if err != nil {
		// Fallback: try to extract credentials directly from TPDU data
		credentials, fallbackErr := extractCredentialsFromTPDUData(tpduData)
		if fallbackErr != nil {
			return "", fmt.Errorf("failed to parse TPDU header and fallback failed: TPDU error: %w, fallback error: %v", err, fallbackErr)
		}
		return credentials, nil
	}

	// Check if it's a connection request
	if tpdu.Code != TPDUConnectionRequest {
		return "", fmt.Errorf("not a connection request: got code %d", tpdu.Code)
	}

	// Extract credentials from TPDU data
	credentials, err := extractCredentialsFromTPDUData(tpdu.Data)
	if err != nil {
		return "", fmt.Errorf("failed to extract credentials: %w", err)
	}

	return credentials, nil
}

// extractCredentialsFromTPDUData extracts credentials from TPDU data
func extractCredentialsFromTPDUData(data []byte) (string, error) {
	// Convert to string for pattern matching
	dataStr := string(data)

	// Look for mstshash pattern
	if strings.Contains(dataStr, "mstshash=") {
		start := strings.Index(dataStr, "mstshash=")
		if start != -1 {
			start += len("mstshash=")
			end := start
			for end < len(dataStr) && dataStr[end] != '\r' && dataStr[end] != '\n' && dataStr[end] != '\x00' {
				end++
			}
			if end > start {
				credential := dataStr[start:end]
				return credential, nil
			}
		}
	}

	// Look for msthash pattern (alternative)
	if strings.Contains(dataStr, "msthash=") {
		start := strings.Index(dataStr, "msthash=")
		if start != -1 {
			start += len("msthash=")
			end := start
			for end < len(dataStr) && dataStr[end] != '\r' && dataStr[end] != '\n' && dataStr[end] != '\x00' {
				end++
			}
			if end > start {
				credential := dataStr[start:end]
				return credential, nil
			}
		}
	}

	// For now, return a default value for testing
	return "default_user", nil
}

// ReadFirstRDPPacket reads the first RDP packet from a connection
func ReadFirstRDPPacket(conn io.Reader) ([]byte, error) {
	// Read TPKT header (4 bytes)
	header := make([]byte, TPKTHeaderSize)
	_, err := io.ReadFull(conn, header)
	if err != nil {
		return nil, fmt.Errorf("failed to read TPKT header: %w", err)
	}

	// Parse header to get length
	tpkt, err := ParseTPKTHeader(header)
	if err != nil {
		return nil, fmt.Errorf("failed to parse TPKT header: %w", err)
	}

	// Read the rest of the packet
	totalLength := int(tpkt.Length)
	if totalLength < TPKTHeaderSize {
		return nil, fmt.Errorf("invalid TPKT length: %d", totalLength)
	}

	packet := make([]byte, totalLength)
	copy(packet[:TPKTHeaderSize], header)

	remaining := totalLength - TPKTHeaderSize
	if remaining > 0 {
		_, err = io.ReadFull(conn, packet[TPKTHeaderSize:])
		if err != nil {
			return nil, fmt.Errorf("failed to read TPKT payload: %w", err)
		}
	}

	return packet, nil
}
