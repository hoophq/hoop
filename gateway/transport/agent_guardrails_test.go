package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
)

func TestAgentGuardrailsDatabaseSessionID(t *testing.T) {
	route := &broker.SessionAuditRoute{
		DatabaseSessionID: "audit-session-id",
		OrgID:             "org-id",
	}

	got, ok := agentGuardrailsDatabaseSessionID(route)
	if !ok {
		t.Fatal("expected a database session ID")
	}
	if got != route.DatabaseSessionID {
		t.Fatalf("expected database session ID %q, got %q", route.DatabaseSessionID, got)
	}

	route.DatabaseSessionID = ""
	if _, ok := agentGuardrailsDatabaseSessionID(route); ok {
		t.Fatal("missing database session ID must not be accepted")
	}
}

func TestBuildLegacyGuardRailErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		items    []models.SessionGuardRailsInfo
		expected string
	}{
		{
			name: "multiple rules, one with a custom message",
			items: []models.SessionGuardRailsInfo{
				{
					RuleName: "Sensitive Data Test",
					Rule: models.SessionGuardRailMatchedRule{
						Type:  "deny_words_list",
						Words: []string{"DENYWORD"},
					},
					Direction:    "input",
					MatchedWords: []string{"DENYWORD"},
					Message:      "You can't use DENYWORD here",
				},
				{
					RuleName: "Sensitive Data Test",
					Rule: models.SessionGuardRailMatchedRule{
						Type:  "deny_words_list",
						Words: []string{"OPENAI"},
					},
					Direction:    "output",
					MatchedWords: []string{"OPENAI"},
				},
				{
					RuleName: "Sensitive Data Test",
					Rule: models.SessionGuardRailMatchedRule{
						Type:         "pattern_match",
						PatternRegex: "TESKE.*",
					},
					Direction:    "input",
					MatchedWords: []string{"TESKE.*"},
				},
			},
			expected: "Blocked because 3 Guardrails rules were violated: " +
				"You can't use DENYWORD here, match guard rail [InputRules:Sensitive Data Test] rule, type=deny_words_list, words=[DENYWORD]; " +
				"match guard rail [OutputRules:Sensitive Data Test] rule, type=deny_words_list, words=[OPENAI]; " +
				"match guard rail [InputRules:Sensitive Data Test] rule, type=pattern_match, patterns=TESKE.*",
		},
		{
			name: "single rule without custom message",
			items: []models.SessionGuardRailsInfo{
				{
					RuleName: "Sensitive Data Test",
					Rule: models.SessionGuardRailMatchedRule{
						Type:  "deny_words_list",
						Words: []string{"DENYWORD"},
					},
					Direction:    "input",
					MatchedWords: []string{"DENYWORD"},
				},
			},
			expected: "Blocked by the following Guardrails rule: " +
				"match guard rail [InputRules:Sensitive Data Test] rule, type=deny_words_list, words=[DENYWORD]",
		},
		{
			name: "single rule with custom message",
			items: []models.SessionGuardRailsInfo{
				{
					RuleName: "PII Guard",
					Rule: models.SessionGuardRailMatchedRule{
						Type:         "pattern_match",
						PatternRegex: "[A-Z0-9]+",
					},
					Direction: "output",
					Message:   "This response was blocked by your organization's data policy",
				},
			},
			expected: "Blocked by the following Guardrails rule: " +
				"This response was blocked by your organization's data policy, " +
				"match guard rail [OutputRules:PII Guard] rule, type=pattern_match, patterns=[A-Z0-9]+",
		},
		{
			name: "single rule without name or configuration",
			items: []models.SessionGuardRailsInfo{
				{
					Rule: models.SessionGuardRailMatchedRule{
						Type: "deny_words_list",
					},
					Direction: "input",
				},
			},
			expected: "Blocked by the following Guardrails rule: " +
				"match guard rail [InputRules] rule, type=deny_words_list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.items)
			if err != nil {
				t.Fatalf("unexpected marshal error: %v", err)
			}

			msg, ok := buildLegacyGuardRailErrorMessage(raw)
			if !ok {
				t.Fatalf("expected message to be rebuilt")
			}
			if msg != tt.expected {
				t.Fatalf("unexpected rebuilt message\nexpected: %s\nactual:   %s", tt.expected, msg)
			}
		})
	}
}

func TestEncodeGuardRailRules(t *testing.T) {
	t.Run("nil rules yield no payload", func(t *testing.T) {
		payload, err := encodeGuardRailRules(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if payload != nil {
			t.Fatalf("expected nil payload, got %q", string(payload))
		}
	})

	// services.GetGuardRailRulesForConnection fabricates "[]" rule sets for
	// connections WITHOUT guardrails. These must not produce a payload —
	// otherwise the fail-closed admission check (DEP-48) refuses ruleless
	// sessions on types without guardrail enforcement.
	t.Run("fabricated empty-array rules yield no payload", func(t *testing.T) {
		payload, err := encodeGuardRailRules(&models.ConnectionGuardRailRules{
			GuardRailInputRules:  []byte("[]"),
			GuardRailOutputRules: []byte("[]"),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if payload != nil {
			t.Fatalf("expected nil payload for empty rules, got %q", string(payload))
		}
	})

	t.Run("attached rules with empty directions yield no payload", func(t *testing.T) {
		payload, err := encodeGuardRailRules(&models.ConnectionGuardRailRules{
			GuardRailInputRules:  []byte(`[{"rules":[]}]`),
			GuardRailOutputRules: []byte(`[{"rules":[]}]`),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if payload != nil {
			t.Fatalf("expected nil payload for empty rule directions, got %q", string(payload))
		}
	})

	t.Run("absent rule columns yield no payload", func(t *testing.T) {
		payload, err := encodeGuardRailRules(&models.ConnectionGuardRailRules{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if payload != nil {
			t.Fatalf("expected nil payload, got %q", string(payload))
		}
	})

	t.Run("real rules yield a payload", func(t *testing.T) {
		inputRules := []byte(`[{"rules":[{"type":"deny_words_list","words":["DENYWORD"]}]}]`)
		payload, err := encodeGuardRailRules(&models.ConnectionGuardRailRules{
			GuardRailInputRules:  inputRules,
			GuardRailOutputRules: []byte(`[{"rules":[]}]`),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(payload) == 0 {
			t.Fatal("expected non-empty payload")
		}
		var decoded struct {
			InputRules  []json.RawMessage `json:"input_rules"`
			OutputRules []json.RawMessage `json:"output_rules"`
		}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("payload is not valid JSON: %v", err)
		}
		if len(decoded.InputRules) != 1 {
			t.Fatalf("expected 1 input rule, got %d", len(decoded.InputRules))
		}
		if len(decoded.OutputRules) != 0 {
			t.Fatalf("expected no output rules, got %d", len(decoded.OutputRules))
		}
	})

	t.Run("invalid rules yield an error", func(t *testing.T) {
		if _, err := encodeGuardRailRules(&models.ConnectionGuardRailRules{
			GuardRailInputRules: []byte("{bad-json"),
		}); err == nil {
			t.Fatal("expected error for invalid rules JSON")
		}
	})
}

func TestBuildLegacyGuardRailErrorMessage_InvalidPayload(t *testing.T) {
	msg, ok := buildLegacyGuardRailErrorMessage([]byte("{bad-json"))
	if ok || msg != "" {
		t.Fatalf("expected no message for invalid payload, got ok=%v msg=%q", ok, msg)
	}
}

func TestUpdateGuardRailsInfoFromPacketSkipsEmptyData(t *testing.T) {
	for name, raw := range map[string][]byte{
		"absent":     nil,
		"empty list": []byte("[]"),
	} {
		t.Run(name, func(t *testing.T) {
			pkt := &pb.Packet{Spec: map[string][]byte{
				pb.SpecClientGuardRailsInfoKey: raw,
			}}
			if updateGuardRailsInfoFromPacket(nil, pkt) {
				t.Fatal("empty guardrails metadata must not be persisted")
			}
		})
	}
}

func TestRewritePGGuardRailsErrorPacket(t *testing.T) {
	items := []models.SessionGuardRailsInfo{
		{
			RuleName: "Sensitive Data Test",
			Rule: models.SessionGuardRailMatchedRule{
				Type:  "deny_words_list",
				Words: []string{"OPENAI"},
			},
			Direction: "output",
			Message:   "Contact #dba before querying this dataset",
		},
	}
	raw, _ := json.Marshal(items)

	pkt := &pb.Packet{
		Type:    "PGConnectionWrite",
		Payload: pgtypes.NewError("%s", "guardrails validation failed").Encode(),
		Spec: map[string][]byte{
			pb.SpecClientGuardRailsInfoKey: raw,
		},
	}

	rewritePGGuardRailsErrorPacket(pkt)
	decoded, err := pgtypes.Decode(bytes.NewBuffer(pkt.Payload))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if decoded.Type() != pgtypes.ServerErrorResponse {
		t.Fatalf("expected server error response packet, got %v", decoded.Type())
	}
	frame := string(decoded.Frame())
	if !strings.Contains(frame, "Blocked by the following Guardrails rule") {
		t.Fatalf("expected rewritten guardrails message, got frame=%q", frame)
	}
	if !strings.Contains(frame, "Contact #dba before querying this dataset") {
		t.Fatalf("expected custom rule message in frame, got frame=%q", frame)
	}
}

func TestDecodeAgentGuardrailsViolationBoundsWork(t *testing.T) {
	valid := []byte(`{"kind":"detection","entity_types":["PERSON"],"detections":[]}`)
	report, err := decodeAgentGuardrailsViolation(valid)
	if err != nil {
		t.Fatalf("decode valid report: %v", err)
	}
	if report.Kind != "detection" {
		t.Fatalf("kind=%q, want detection", report.Kind)
	}

	if _, err := decodeAgentGuardrailsViolation(make([]byte, maxAgentGuardrailsPayload+1)); err == nil {
		t.Fatal("oversized report payload was accepted")
	}

	tooMany := agentGuardrailsViolation{
		Kind:       "detection",
		Detections: make([]agentGuardrailsDetection, maxAgentGuardrailsDetections+1),
	}
	payload, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatalf("marshal oversized detection list: %v", err)
	}
	if len(payload) > maxAgentGuardrailsPayload {
		t.Fatalf("test payload unexpectedly exceeds byte cap: %d", len(payload))
	}
	if _, err := decodeAgentGuardrailsViolation(payload); err == nil {
		t.Fatal("oversized detection list was accepted")
	}
}

func TestAgentGuardrailsProcessorRejectsFullQueue(t *testing.T) {
	processor := &agentGuardrailsProcessor{
		queue: make(chan agentGuardrailsWork, 1),
	}
	if err := processor.submit(agentGuardrailsWork{}); err != nil {
		t.Fatalf("first admission: %v", err)
	}
	if err := processor.submit(agentGuardrailsWork{}); !errors.Is(err, errAgentGuardrailsQueueFull) {
		t.Fatalf("second admission error=%v, want queue full", err)
	}
	close(processor.queue)
}

func TestHandleWebSocketMessageTreatsLeadingBraceAsRaw(t *testing.T) {
	sid := uuid.New()
	payload := []byte{'{', 0xff, 0x00}
	header := (&broker.Header{SID: sid, Len: uint32(len(payload))}).Encode()
	frame := append(header, payload...)

	if err := handleWebSocketMessage("agent", uuid.New(), frame, nil); err != nil {
		t.Fatalf("target-controlled raw bytes closed the shared relay: %v", err)
	}
}

func TestDecodeAgentControlFrameSupportsLegacyUntilV2(t *testing.T) {
	const agentName = "legacy-frame-agent"
	instanceID, err := broker.CreateAgent(agentName, nil)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	defer broker.RemoveAgent(agentName, instanceID)

	message := broker.WebSocketMessage{
		Type:     broker.MessageTypeCapabilities,
		Metadata: map[string]string{broker.CapabilitySupportsPIIGuard: "false"},
		Payload:  []byte{},
	}
	payload, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal legacy envelope: %v", err)
	}
	header := &broker.Header{
		SID: broker.ControlSentinelSID,
		Len: uint32(len(payload)),
	}

	decoded, control, err := decodeAgentControlFrame(
		agentName,
		instanceID,
		header,
		payload,
	)
	if err != nil {
		t.Fatalf("decode legacy envelope: %v", err)
	}
	if !control || decoded.Type != broker.MessageTypeCapabilities {
		t.Fatalf("legacy envelope was not recognized: control=%v msg=%+v", control, decoded)
	}

	broker.SetAgentCapabilities(agentName, instanceID, map[string]string{
		broker.CapabilityFrameProtocol: broker.FrameProtocolV2,
	})
	_, control, err = decodeAgentControlFrame(agentName, instanceID, header, payload)
	if err != nil {
		t.Fatalf("classify post-negotiation frame: %v", err)
	}
	if control {
		t.Fatal("legacy envelope remained enabled after v2 negotiation")
	}
}

func TestCapabilityHandshakeAcknowledgesBeforeEnablingV2(t *testing.T) {
	type upgradeResult struct {
		conn *websocket.Conn
		err  error
	}
	upgraded := make(chan upgradeResult, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		upgraded <- upgradeResult{conn: conn, err: err}
		if err == nil {
			<-release
			_ = conn.Close()
		}
	}))

	client, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http"),
		nil,
	)
	if err != nil {
		close(release)
		server.Close()
		t.Fatalf("dial test WebSocket: %v", err)
	}
	result := <-upgraded
	if result.err != nil {
		_ = client.Close()
		close(release)
		server.Close()
		t.Fatalf("upgrade test WebSocket: %v", result.err)
	}

	const agentName = "frame-handshake-agent"
	instanceID, err := broker.CreateAgent(agentName, result.conn)
	if err != nil {
		_ = client.Close()
		close(release)
		server.Close()
		t.Fatalf("CreateAgent: %v", err)
	}
	defer func() {
		broker.RemoveAgent(agentName, instanceID)
		_ = client.Close()
		close(release)
		server.Close()
	}()

	capabilities := &broker.WebSocketMessage{
		Type: broker.MessageTypeCapabilities,
		Metadata: map[string]string{
			broker.CapabilityFrameProtocol: broker.FrameProtocolV2,
		},
		Payload: []byte{},
	}
	frame, err := broker.EncodeWebSocketMessage(broker.ControlSentinelSID, capabilities)
	if err != nil {
		t.Fatalf("encode capability frame: %v", err)
	}
	if err := handleWebSocketMessage(agentName, instanceID, frame, nil); err != nil {
		t.Fatalf("handle capability frame: %v", err)
	}

	messageType, ackFrame, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("read capability acknowledgement: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("acknowledgement message type=%d, want binary", messageType)
	}
	sid, ack, err := broker.DecodeWebSocketMessage(ackFrame)
	if err != nil {
		t.Fatalf("decode capability acknowledgement: %v", err)
	}
	if sid != broker.ControlSentinelSID ||
		ack.Type != broker.MessageTypeCapabilities ||
		ack.Metadata[broker.CapabilityFrameProtocol] != broker.FrameProtocolV2 {
		t.Fatalf("unexpected capability acknowledgement: sid=%s msg=%+v", sid, ack)
	}
	if !broker.AgentUsesFrameProtocolV2(agentName, instanceID) {
		t.Fatal("v2 mode was not published after acknowledgement")
	}
}

func TestHandleWebSocketMessageRejectsLengthMismatch(t *testing.T) {
	sid := uuid.New()
	payload := []byte{0x03, 0x00}
	header := (&broker.Header{SID: sid, Len: 1}).Encode()
	frame := append(header, payload...)

	if err := handleWebSocketMessage("agent", uuid.New(), frame, nil); err == nil {
		t.Fatal("mismatched declared payload length was accepted")
	}
}

func TestHandleWebSocketMessageRejectsOversizedControl(t *testing.T) {
	sid := uuid.New()
	payload := make([]byte, maxAgentControlFrameBytes+1)
	payload[0] = '{'
	header := (&broker.Header{
		SID:     sid,
		Len:     uint32(len(payload)),
		Control: true,
	}).Encode()
	frame := append(header, payload...)

	if err := handleWebSocketMessage("agent", uuid.New(), frame, nil); err == nil {
		t.Fatal("oversized control frame was accepted")
	}
}

func TestAgentWebSocketDoesNotNegotiateCompression(t *testing.T) {
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		serverErr <- conn.Close()
	}))
	defer server.Close()

	dialer := websocket.Dialer{EnableCompression: true}
	conn, response, err := dialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http"),
		http.Header{},
	)
	if err != nil {
		t.Fatalf("dial test WebSocket: %v", err)
	}
	defer conn.Close()

	if extensions := response.Header.Get("Sec-WebSocket-Extensions"); strings.Contains(
		strings.ToLower(extensions),
		"permessage-deflate",
	) {
		t.Fatalf("agent relay negotiated compression: %q", extensions)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test WebSocket server: %v", err)
	}
}
