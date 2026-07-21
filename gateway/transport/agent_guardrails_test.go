package transport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/common/featureflag"
	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

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

	// MySQL, MSSQL and MongoDB native proxies are not unconditionally supported
	// by connection type. MySQL/MongoDB native proxies do not evaluate
	// guardrails at all (fail closed, DEP-48). MSSQL native IS supported, but
	// only when its feature flag is on, which is decided per-session in
	// sessionSupportsGuardRails (it needs the org id) rather than here.
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

func TestSessionSupportsGuardRails(t *testing.T) {
	const flag = "beta.mssql_native_guardrails"
	tests := []struct {
		name   string
		orgID  string
		flagOn bool
		ctx    plugintypes.Context
		want   bool
	}{
		{
			name:   "mssql native session, flag on",
			orgID:  "org-mssql-on",
			flagOn: true,
			ctx: plugintypes.Context{
				OrgID:          "org-mssql-on",
				ConnectionType: string(pb.ConnectionTypeMSSQL),
				ClientVerb:     pb.ClientVerbConnect,
				ClientOrigin:   pb.ConnectionOriginClient,
			},
			want: true,
		},
		{
			name:   "mssql native session, flag off",
			orgID:  "org-mssql-off",
			flagOn: false,
			ctx: plugintypes.Context{
				OrgID:          "org-mssql-off",
				ConnectionType: string(pb.ConnectionTypeMSSQL),
				ClientVerb:     pb.ClientVerbConnect,
				ClientOrigin:   pb.ConnectionOriginClient,
			},
			want: false,
		},
		{
			name:   "mssql web exec is supported regardless of flag",
			orgID:  "org-mssql-off2",
			flagOn: false,
			ctx: plugintypes.Context{
				OrgID:          "org-mssql-off2",
				ConnectionType: string(pb.ConnectionTypeMSSQL),
				ClientVerb:     pb.ClientVerbExec,
				ClientOrigin:   pb.ConnectionOriginClientAPI,
			},
			want: true,
		},
		{
			name:   "mssql non-exec API session, flag off",
			orgID:  "org-mssql-off3",
			flagOn: false,
			ctx: plugintypes.Context{
				OrgID:          "org-mssql-off3",
				ConnectionType: string(pb.ConnectionTypeMSSQL),
				ClientVerb:     pb.ClientVerbPlainExec,
				ClientOrigin:   pb.ConnectionOriginClientAPI,
			},
			want: false,
		},
		{
			name:  "postgres native is supported by type",
			orgID: "org-pg",
			ctx: plugintypes.Context{
				OrgID:          "org-pg",
				ConnectionType: string(pb.ConnectionTypePostgres),
				ClientVerb:     pb.ClientVerbConnect,
				ClientOrigin:   pb.ConnectionOriginClient,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			featureflag.Set(tt.orgID, flag, tt.flagOn)
			if got := sessionSupportsGuardRails(tt.ctx); got != tt.want {
				t.Fatalf("sessionSupportsGuardRails() = %v, want %v", got, tt.want)
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
