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
