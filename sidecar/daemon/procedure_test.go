package daemon

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// A strict table rule with no operations scope refuses every statement the
// scanner found no relation in: a stored-procedure call, a driver's SET.
// The config loads, and the lane says so where -validate reads it.
func TestUnscopedRequireTableMatchIsNoted(t *testing.T) {
	noted := func(t *testing.T, r policy.Rule) bool {
		t.Helper()
		cfg := pgLane(r)
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		lanes, err := buildLanes(cfg, nil, nil)
		if err != nil {
			t.Fatalf("buildLanes: %v", err)
		}
		for _, n := range lanes[0].notes {
			if strings.Contains(n, "require_table_match") && strings.Contains(n, "stored-procedure") {
				return true
			}
		}
		return false
	}
	strict := policy.Rule{Name: "fence", Type: policy.MatchTable, Tables: []string{"customers"}, RequireTableMatch: true}
	if !noted(t, strict) {
		t.Error("an unscoped require_table_match rule left no note")
	}
	scoped := strict
	scoped.Operations = []inspect.Operation{inspect.OpDelete, inspect.OpUpdate}
	if noted(t, scoped) {
		t.Error("a scoped rule was noted; the scope is the fix the note asks for")
	}
	loose := strict
	loose.RequireTableMatch = false
	if noted(t, loose) {
		t.Error("a rule without require_table_match was noted")
	}
}

// The rule type loads through the daemon and reaches the capability header,
// so a control plane serves it only to a build that evaluates it.
func TestProcedureRuleLoadsAndIsAdvertised(t *testing.T) {
	cfg := pgLane(policy.Rule{Name: "no-secrets", Type: policy.MatchProcedure, Procedures: []string{"get_secrets"}})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	v := lanes[0].policy.Evaluate(inspect.Statement{
		Protocol: inspect.Postgres, Direction: inspect.FromClient,
		Text: "CALL app.get_secrets()", Operation: inspect.OpUnknown,
	})
	if !v.Denied || v.Rule != "no-secrets" {
		t.Errorf("verdict = %+v, want denied by no-secrets", v)
	}
	if err := CheckServable(*cfg, Handshake{Capabilities: []string{"rule:table", "protocol:postgres"}}); err == nil ||
		!strings.Contains(err.Error(), "procedure") {
		t.Errorf("a build without rule:procedure was served the rule: %v", err)
	}
	if err := CheckServable(*cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("this build refused its own rule: %v", err)
	}
}
