package broker

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDecodeFrameRequiresExactDeclaredLength(t *testing.T) {
	sid := uuid.New()
	payload := []byte("relay payload")
	header := (&Header{SID: sid, Len: uint32(len(payload))}).Encode()
	frame := append(header, payload...)

	decodedHeader, decodedPayload, err := DecodeFrame(frame)
	if err != nil {
		t.Fatalf("decode valid frame: %v", err)
	}
	if decodedHeader.SID != sid || !bytes.Equal(decodedPayload, payload) {
		t.Fatalf("decoded frame mismatch: header=%+v payload=%q", decodedHeader, decodedPayload)
	}
	if decodedHeader.Control {
		t.Fatal("raw relay frame decoded as control")
	}

	for name, mutate := range map[string]func([]byte) []byte{
		"declared short": func(data []byte) []byte {
			binary.BigEndian.PutUint32(data[16:HeaderSize], uint32(len(payload)-1))
			return data
		},
		"declared long": func(data []byte) []byte {
			binary.BigEndian.PutUint32(data[16:HeaderSize], uint32(len(payload)+1))
			return data
		},
		"trailing byte": func(data []byte) []byte {
			return append(data, 0)
		},
		"truncated byte": func(data []byte) []byte {
			return data[:len(data)-1]
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := mutate(bytes.Clone(frame))
			if _, _, err := DecodeFrame(candidate); err == nil {
				t.Fatal("expected payload length mismatch")
			}
		})
	}
}

func TestFrameKindIsExplicitOnTheWire(t *testing.T) {
	sid := uuid.New()
	msg := &WebSocketMessage{
		Type:     MessageTypeData,
		Metadata: map[string]string{},
		Payload:  []byte("payload"),
	}
	frame, err := EncodeWebSocketMessage(sid, msg)
	if err != nil {
		t.Fatalf("encode control frame: %v", err)
	}
	header, _, err := DecodeFrame(frame)
	if err != nil {
		t.Fatalf("decode control frame: %v", err)
	}
	if !header.Control {
		t.Fatal("control frame lost its wire discriminator")
	}

	rawPayload := []byte{'{', 0xff, 0x00}
	rawFrame := append((&Header{SID: sid, Len: uint32(len(rawPayload))}).Encode(), rawPayload...)
	header, decoded, err := DecodeFrame(rawFrame)
	if err != nil {
		t.Fatalf("decode raw frame: %v", err)
	}
	if header.Control || !bytes.Equal(decoded, rawPayload) {
		t.Fatalf("raw frame misclassified: header=%+v payload=%x", header, decoded)
	}
}

func TestAgentControlEncodingNegotiatesFromLegacyToV2(t *testing.T) {
	const agentName = "frame-negotiation-agent"
	instanceID, err := CreateAgent(agentName, nil)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	defer BrokerInstance.agents.Delete(agentName)

	msg := &WebSocketMessage{
		Type:     MessageTypeSessionStarted,
		Metadata: map[string]string{},
		Payload:  []byte{},
	}
	legacy, err := EncodeWebSocketMessageForAgent(agentName, instanceID, uuid.New(), msg)
	if err != nil {
		t.Fatalf("encode legacy control: %v", err)
	}
	legacyHeader, _, err := DecodeFrame(legacy)
	if err != nil {
		t.Fatalf("decode legacy control: %v", err)
	}
	if legacyHeader.Control {
		t.Fatal("unknown peer received a v2 control frame")
	}

	SetAgentCapabilities(agentName, instanceID, map[string]string{
		CapabilityFrameProtocol: FrameProtocolV2,
	})
	typed, err := EncodeWebSocketMessageForAgent(agentName, instanceID, uuid.New(), msg)
	if err != nil {
		t.Fatalf("encode v2 control: %v", err)
	}
	typedHeader, _, err := DecodeFrame(typed)
	if err != nil {
		t.Fatalf("decode v2 control: %v", err)
	}
	if !typedHeader.Control {
		t.Fatal("negotiated peer did not receive a v2 control frame")
	}
}

// An oversized payload must fail the affected send, not take the gateway
// process down. Header.Encode still panics on a length it cannot represent,
// so SendRawDataToAgent — the one framing path whose length comes from a peer
// read — must reject the frame before reaching it.
func TestSendRawDataToAgentRejectsOversizedFrameWithoutPanicking(t *testing.T) {
	oversized := uint64(headerLengthMask) + 1
	if uint64(int(^uint(0)>>1)) < oversized {
		t.Skip("platform int cannot express an oversized frame")
	}

	session := &Session{ID: uuid.New()}
	err := session.SendRawDataToAgent(make([]byte, oversized))
	if err == nil {
		t.Fatal("oversized frame was accepted; expected a wire-limit error")
	}
	if !strings.Contains(err.Error(), "exceeds wire limit") {
		t.Fatalf("expected a wire-limit error, got: %v", err)
	}
}
