package policy

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestGRPCStatusRule(t *testing.T) {
	rules, err := NewRules([]Rule{{
		Name: "auth-failure", Type: MatchGRPCStatus,
		httpRuleFields: httpRuleFields{
			Statuses: []string{"permission_denied", "16"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name   string
		status string
		deny   bool
	}{
		{name: "named match", status: "7", deny: true},
		{name: "numeric spec match", status: "16", deny: true},
		{name: "different status", status: "0", deny: false},
		{name: "request has no status", status: "", deny: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			metadata := map[string]string{}
			if tt.status != "" {
				metadata[inspect.MetadataGRPCStatusCode] = tt.status
			}
			got := rules.Evaluate(inspect.Statement{Protocol: inspect.GRPC, Metadata: metadata})
			if got.Denied != tt.deny {
				t.Fatalf("Denied = %v, want %v", got.Denied, tt.deny)
			}
		})
	}
}

func TestGRPCStatusRuleRejectsUnknownStatus(t *testing.T) {
	_, err := NewRules([]Rule{{
		Name: "bad-status", Type: MatchGRPCStatus,
		httpRuleFields: httpRuleFields{
			Statuses: []string{"not_a_status"},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "invalid grpc status") {
		t.Fatalf("error = %v", err)
	}
}
