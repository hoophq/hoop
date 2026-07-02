package transport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
)

func TestBuildLegacyGuardRailErrorMessage(t *testing.T) {
	items := []models.SessionGuardRailsInfo{
		{
			RuleName: "Sensitive Data Test",
			Rule: models.SessionGuardRailMatchedRule{
				Type:  "deny_words_list",
				Words: []string{"DENYWORD"},
			},
			Direction:    "input",
			MatchedWords: []string{"DENYWORD"},
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
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	msg, ok := buildLegacyGuardRailErrorMessage(raw)
	if !ok {
		t.Fatalf("expected message to be rebuilt")
	}

	expected := "Blocked by the following Hoop Guardrails Rules: " +
		"validation error, match guard rail [InputRules:Sensitive Data Test] rule, type=deny_words_list, words=[DENYWORD], " +
		"validation error, match guard rail [OutputRules:Sensitive Data Test] rule, type=deny_words_list, words=[OPENAI], " +
		"validation error, match guard rail [InputRules:Sensitive Data Test] rule, type=pattern_match, patterns=TESKE.*"

	if msg != expected {
		t.Fatalf("unexpected rebuilt message\nexpected: %s\nactual:   %s", expected, msg)
	}
}

func TestConnectionTypeSupportsGuardRails(t *testing.T) {
	supported := []pb.ConnectionType{
		pb.ConnectionTypePostgres,
		pb.ConnectionTypeOracleDB,
		pb.ConnectionTypeHttpProxy,
		pb.ConnectionTypeSSH,
		pb.ConnectionTypeCommandLine,
	}
	for _, ct := range supported {
		if !connectionTypeSupportsGuardRails(ct) {
			t.Errorf("expected connection type %q to support guardrails", ct)
		}
	}

	// MySQL, MSSQL and MongoDB proxies do not evaluate guardrails yet, so a
	// guarded session of these types must be refused (fail closed), not run
	// unguarded (DEP-48).
	unsupported := []pb.ConnectionType{
		pb.ConnectionTypeMySQL,
		pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMongoDB,
		pb.ConnectionTypeTCP,
	}
	for _, ct := range unsupported {
		if connectionTypeSupportsGuardRails(ct) {
			t.Errorf("expected connection type %q to NOT support guardrails", ct)
		}
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
		inputRules := []byte(`[{"items":[{"type":"deny_words_list","words":["DENYWORD"]}]}]`)
		payload, err := encodeGuardRailRules(&models.ConnectionGuardRailRules{
			GuardRailInputRules:  inputRules,
			GuardRailOutputRules: []byte("[]"),
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

func TestRewritePGGuardRailsErrorPacket(t *testing.T) {
	items := []models.SessionGuardRailsInfo{
		{
			RuleName: "Sensitive Data Test",
			Rule: models.SessionGuardRailMatchedRule{
				Type:  "deny_words_list",
				Words: []string{"OPENAI"},
			},
			Direction: "output",
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
	if !strings.Contains(string(decoded.Frame()), "Blocked by the following Hoop Guardrails Rules") {
		t.Fatalf("expected rewritten legacy message, got frame=%q", string(decoded.Frame()))
	}
}
