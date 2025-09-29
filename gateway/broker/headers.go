package broker

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// Header represents the session header for framing
type Header struct {
	SID uuid.UUID
	Len uint32
}

// WebSocketMessage represents a flexible message format for different protocols
type WebSocketMessage struct {
	Type     string            `json:"type"`
	Metadata map[string]string `json:"metadata"`
	Payload  []byte            `json:"payload"`
}

// Message types
const (
	MessageTypeSessionStarted = "session_started"
	MessageTypeData           = "data"
)

func (h *Header) Encode() []byte {
	if h.SID == uuid.Nil {
		panic("cannot encode nil UUID")
	}

	buf := make([]byte, 20) // 16 bytes for UUID + 4 bytes for length
	copy(buf[:16], h.SID[:])
	binary.BigEndian.PutUint32(buf[16:20], h.Len)
	return buf
}

// Decode decodes a header from bytes
func DecodeHeader(data []byte) (*Header, int, error) {
	if len(data) < 20 {
		return nil, 0, fmt.Errorf("insufficient data for header")
	}

	// Use FromBytes to validate the UUID bytes
	sid, err := uuid.FromBytes(data[:16])
	if err != nil {
		return nil, 0, fmt.Errorf("invalid UUID in header: %w", err)
	}

	if sid == uuid.Nil {
		return nil, 0, fmt.Errorf("nil UUID not allowed")
	}

	// Validate UUID version (optional)
	if sid.Version() == 0 {
		return nil, 0, fmt.Errorf("invalid UUID: version 0 not allowed")
	}

	len := binary.BigEndian.Uint32(data[16:20])

	return &Header{
		SID: sid,
		Len: len,
	}, 20, nil
}

// EncodeWebSocketMessage encodes a WebSocketMessage to bytes with header
func EncodeWebSocketMessage(sessionID uuid.UUID, msg *WebSocketMessage) ([]byte, error) {
	jsonData, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal WebSocketMessage: %w", err)
	}

	// Create header
	header := &Header{
		SID: sessionID,
		Len: uint32(len(jsonData)),
	}

	// Combine header + JSON data
	result := make([]byte, 20+len(jsonData))
	copy(result[:20], header.Encode())
	copy(result[20:], jsonData)

	return result, nil
}

// DecodeWebSocketMessage decodes bytes to WebSocketMessage
func DecodeWebSocketMessage(data []byte) (uuid.UUID, *WebSocketMessage, error) {
	header, headerLen, err := DecodeHeader(data)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to decode header: %w", err)
	}

	// Extract JSON payload
	if len(data) < headerLen {
		return uuid.Nil, nil, fmt.Errorf("insufficient data for payload")
	}

	jsonData := data[headerLen:]
	var msg WebSocketMessage
	if err := json.Unmarshal(jsonData, &msg); err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to unmarshal WebSocketMessage: %w", err)
	}

	return header.SID, &msg, nil
}
