package analyzer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func httpCall(op inspect.Operation, path string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Text:      strings.ToUpper(string(op)) + " " + path,
		Operation: op,
		HTTP: &inspect.HTTPDetail{
			Method: strings.ToUpper(string(op)), Path: path, Target: path, Resource: path,
		},
	}
}

// classified reports whether the trigger sent stmt to the model.
func classified(t *testing.T, trigger analyzer.Trigger, stmt inspect.Statement) bool {
	t.Helper()
	p := &stubProvider{level: analyzer.RiskLow}
	mustNew(t, analyzer.Config{
		Provider: p,
		Trigger:  trigger,
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
	}).Evaluate(stmt)
	return p.calls.Load() > 0
}

// "Hold patch except on this path" could not be written while every list
// ORed (ADR-0024). An item ANDs its fields; exclude removes a match.
func TestTriggerItemsAndTheirFields(t *testing.T) {
	patchUnderAPI := analyzer.Trigger{Any: []analyzer.TriggerItem{{
		Operations: []inspect.Operation{inspect.OpPatch},
		Resources:  []string{"/api/**"},
	}}}
	for _, tc := range []struct {
		name    string
		trigger analyzer.Trigger
		stmt    inspect.Statement
		want    bool
	}{
		{"both fields match", patchUnderAPI, httpCall(inspect.OpPatch, "/api/users/7"), true},
		{"operation only", patchUnderAPI, httpCall(inspect.OpPatch, "/admin/users/7"), false},
		{"resource only", patchUnderAPI, httpCall(inspect.OpGet, "/api/users/7"), false},
		{"items OR", analyzer.Trigger{Any: []analyzer.TriggerItem{
			{Operations: []inspect.Operation{inspect.OpDelete}},
			{Operations: []inspect.Operation{inspect.OpPatch}},
		}}, httpCall(inspect.OpPatch, "/x"), true},
		{"flat lists OR with items", analyzer.Trigger{
			Operations: []inspect.Operation{inspect.OpDelete},
			Any:        patchUnderAPI.Any,
		}, httpCall(inspect.OpDelete, "/admin"), true},
		{"tables AND operations", analyzer.Trigger{Any: []analyzer.TriggerItem{{
			Operations: []inspect.Operation{inspect.OpDelete}, Tables: []string{"Users"},
		}}}, sqlStmt("DELETE FROM orders", inspect.OpDelete, "orders"), false},
		{"resources never match sql", analyzer.Trigger{Any: []analyzer.TriggerItem{{
			Operations: []inspect.Operation{inspect.OpDelete}, Resources: []string{"/**"},
		}}}, sqlStmt("DELETE FROM users", inspect.OpDelete, "users"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classified(t, tc.trigger, tc.stmt); got != tc.want {
				t.Fatalf("classified = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExcludeRemovesAMatch(t *testing.T) {
	healthz := []analyzer.TriggerItem{{Resources: []string{"/api/healthz"}}}
	for _, tc := range []struct {
		name    string
		trigger analyzer.Trigger
		stmt    inspect.Statement
		want    bool
	}{
		{"flat list", analyzer.Trigger{
			Operations: []inspect.Operation{inspect.OpPatch}, Exclude: healthz,
		}, httpCall(inspect.OpPatch, "/api/healthz"), false},
		{"flat list, other path", analyzer.Trigger{
			Operations: []inspect.Operation{inspect.OpPatch}, Exclude: healthz,
		}, httpCall(inspect.OpPatch, "/api/users"), true},
		{"all", analyzer.Trigger{All: true, Exclude: healthz},
			httpCall(inspect.OpGet, "/api/healthz"), false},
		{"item", analyzer.Trigger{Any: []analyzer.TriggerItem{
			{Resources: []string{"/api/**"}},
		}, Exclude: healthz}, httpCall(inspect.OpGet, "/api/healthz"), false},
		// An exclude item ANDs too: a get on the path is still classified.
		{"exclude ANDs", analyzer.Trigger{All: true, Exclude: []analyzer.TriggerItem{{
			Operations: []inspect.Operation{inspect.OpPatch}, Resources: []string{"/api/healthz"},
		}}}, httpCall(inspect.OpGet, "/api/healthz"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classified(t, tc.trigger, tc.stmt); got != tc.want {
				t.Fatalf("classified = %v, want %v", got, tc.want)
			}
		})
	}
}

// Exclude selects nothing, so the daemon reads an exclude-only trigger as
// "no trigger" and widens it with All on an ungated lane.
func TestExcludeAloneIsAZeroTrigger(t *testing.T) {
	tr := analyzer.Trigger{Exclude: []analyzer.TriggerItem{{Resources: []string{"/x"}}}}
	if !tr.IsZero() {
		t.Fatal("an exclude-only trigger reported that it selects something")
	}
	if classified(t, tr, httpCall(inspect.OpGet, "/y")) {
		t.Fatal("an exclude-only trigger classified a statement")
	}
}

// An item naming no field would match every statement by checking nothing.
func TestAnEmptyTriggerItemIsRefused(t *testing.T) {
	for _, tr := range []analyzer.Trigger{
		{Any: []analyzer.TriggerItem{{}}},
		{All: true, Exclude: []analyzer.TriggerItem{{}}},
	} {
		_, err := analyzer.New(analyzer.Config{
			Provider: &stubProvider{}, Trigger: tr,
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
		})
		if err == nil || !strings.Contains(err.Error(), "names no") {
			t.Fatalf("New(%+v) = %v, want an empty-item refusal", tr, err)
		}
	}
}
