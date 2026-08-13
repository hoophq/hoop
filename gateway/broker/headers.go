package broker

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// Header represents the session header for framing
type Header struct {
	SID     uuid.UUID
	Len     uint32
	Control bool
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
	// MessageTypeGuardrailsViolation is sent by the agent (agentrs) when its
	// PII guard detects a violation or fails closed. The payload carries
	// entity metadata only (types, scores, bounding boxes) — no pixels or
	// recognized text.
	MessageTypeGuardrailsViolation = "guardrails_violation"
	// MessageTypeGuardrailsViolationAck is returned only after the gateway
	// transactionally accepts a report_id.
	MessageTypeGuardrailsViolationAck = "guardrails_violation_ack"
	// MessageTypeCapabilities is sent by the agent (agentrs) once, as the
	// first frame after connecting. It is connection-scoped (no session id):
	// it advertises what this agent can do so the gateway can decide, at
	// session-creation time, whether to delegate work like the PII guard. It
	// is addressed with ControlSentinelSID, not a session id.
	MessageTypeCapabilities = "capabilities"
)

// CapabilitySupportsPIIGuard is the capability key an agent sets to "true"
// when it can honor a delegated PII guard (it has both the Presidio analyzer
// and OCR sidecar endpoints configured).
const CapabilitySupportsPIIGuard = "supports_pii_guard"

// CapabilitySupportsPIIDataMaskingRules distinguishes agents that consume the
// complete connection-scoped Data Masking rule payload, including custom
// regex and deny-list entity types.
const CapabilitySupportsPIIDataMaskingRules = "supports_pii_data_masking_rules"

// CapabilityFrameProtocol negotiates the multiplexed agent frame format.
// Version "2" uses Header.Control (the high bit of the length word). Peers
// remain in bounded legacy-envelope mode until both sides acknowledge it.
const CapabilityFrameProtocol = "frame_protocol"
const FrameProtocolV2 = "2"

// ControlSentinelSID is the well-known sid used for connection-scoped control
// frames (those that describe the agent connection, not a session). The wire
// format requires a non-nil, versioned UUID in every header, so these frames
// borrow this fixed v4 UUID. The gateway dispatches them by message type at
// the connection level — the sid never identifies a real session.
//
// Keep this byte-for-byte identical to the agent constant
// `ws::control::CONTROL_SENTINEL_SID`.
var ControlSentinelSID = uuid.UUID{
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x4c, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
}

const (
	HeaderSize        = 20 // 16 bytes for UUID + 4 bytes for length and kind
	headerControlFlag = uint32(1 << 31)
	headerLengthMask  = ^headerControlFlag
)

func (h *Header) Encode() []byte {
	if h.SID == uuid.Nil {
		panic("cannot encode nil UUID")
	}
	if h.Len > headerLengthMask {
		panic("frame payload exceeds wire limit")
	}
	wireLen := h.Len
	if h.Control {
		wireLen |= headerControlFlag
	}

	buf := make([]byte, HeaderSize)
	copy(buf[:16], h.SID[:])
	binary.BigEndian.PutUint32(buf[16:HeaderSize], wireLen)
	return buf
}

// Decode decodes a header from bytes
func DecodeHeader(data []byte) (*Header, int, error) {
	if len(data) < HeaderSize {
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

	wireLen := binary.BigEndian.Uint32(data[16:HeaderSize])

	return &Header{
		SID:     sid,
		Len:     wireLen & headerLengthMask,
		Control: wireLen&headerControlFlag != 0,
	}, HeaderSize, nil
}

// DecodeFrame validates one complete message and returns its zero-copy payload.
// Header.Len is part of the wire contract; accepting trailing or truncated
// bytes would let a frame be classified differently by control and relay
// decoders.
func DecodeFrame(data []byte) (*Header, []byte, error) {
	header, headerLen, err := DecodeHeader(data)
	if err != nil {
		return nil, nil, err
	}
	payloadLen := len(data) - headerLen
	if uint64(header.Len) != uint64(payloadLen) {
		return nil, nil, fmt.Errorf(
			"payload length mismatch: declared=%d actual=%d",
			header.Len,
			payloadLen,
		)
	}
	return header, data[headerLen:], nil
}

// EncodeWebSocketMessage encodes a v2 control envelope.
func EncodeWebSocketMessage(sessionID uuid.UUID, msg *WebSocketMessage) ([]byte, error) {
	return encodeWebSocketMessage(sessionID, msg, true)
}

// EncodeWebSocketMessageForAgent uses v2 only after the exact connection
// instance advertised it and received the gateway acknowledgement.
func EncodeWebSocketMessageForAgent(
	agentID string,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
	msg *WebSocketMessage,
) ([]byte, error) {
	return encodeWebSocketMessage(
		sessionID,
		msg,
		AgentUsesFrameProtocolV2(agentID, instanceID),
	)
}

func encodeWebSocketMessage(
	sessionID uuid.UUID,
	msg *WebSocketMessage,
	control bool,
) ([]byte, error) {
	jsonData, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal WebSocketMessage: %w", err)
	}

	// Create header
	header := &Header{
		SID:     sessionID,
		Len:     uint32(len(jsonData)),
		Control: control,
	}

	// Combine header + JSON data
	result := make([]byte, HeaderSize+len(jsonData))
	copy(result[:HeaderSize], header.Encode())
	copy(result[HeaderSize:], jsonData)

	return result, nil
}

// DecodeWebSocketMessage decodes bytes to WebSocketMessage
func DecodeWebSocketMessage(data []byte) (uuid.UUID, *WebSocketMessage, error) {
	header, jsonData, err := DecodeFrame(data)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to decode frame: %w", err)
	}
	if !header.Control {
		return uuid.Nil, nil, fmt.Errorf("expected a control frame")
	}

	var msg WebSocketMessage
	if err := json.Unmarshal(jsonData, &msg); err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to unmarshal WebSocketMessage: %w", err)
	}

	return header.SID, &msg, nil
}
