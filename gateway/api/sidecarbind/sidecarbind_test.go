package sidecarbind

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

// TestEffectiveTargetsKeepsTheBindingsARequestDoesNotMention is the guard on
// the whole write gate.
//
// An edit that says nothing about sidecars still changes what the fleet
// enforces: the spec it stores is served to every listener the rule is already
// bound to. Checking the request's own (absent) list instead lets a rule be
// edited past every protocol, listener and cap refusal while the bindings go
// on delivering it -- and the failure shows up as a sidecar refusing its whole
// configuration at its next restart.
func TestEffectiveTargetsKeepsTheBindingsARequestDoesNotMention(t *testing.T) {
	bound := []models.SidecarRuleTarget{{SidecarID: "sc-1", ListenerName: "appdb"}}
	stored := func() ([]models.SidecarRuleTarget, error) { return bound, nil }

	got, err := effectiveTargets(nil, stored)
	if err != nil {
		t.Fatalf("an absent target list: %v", err)
	}
	if len(got) != 1 || got[0].ListenerName != "appdb" {
		t.Errorf("an absent list must validate against the stored bindings, got %+v", got)
	}

	// An explicit empty list is the admin unbinding, and it must NOT fall back
	// to what is stored -- that is the one write whose whole point is to
	// remove them.
	empty := []openapi.SidecarRuleTarget{}
	got, err = effectiveTargets(&empty, stored)
	if err != nil || len(got) != 0 {
		t.Errorf("an explicit [] must unbind, got %+v (err %v)", got, err)
	}

	// A named list replaces, and never reads the stored one.
	named := []openapi.SidecarRuleTarget{{SidecarID: "sc-2", ListenerName: "reporting"}}
	got, err = effectiveTargets(&named, func() ([]models.SidecarRuleTarget, error) {
		t.Error("a request that names targets must not read the stored ones")
		return nil, nil
	})
	if err != nil || len(got) != 1 || got[0].SidecarID != "sc-2" {
		t.Errorf("a named list must replace, got %+v (err %v)", got, err)
	}

	// A target with no sidecar is refused, not dropped. Dropped, a list of
	// nothing but malformed entries reads as "unbind everywhere" and a client
	// bug deletes a fleet's bindings with a 200.
	bad := []openapi.SidecarRuleTarget{{ListenerName: "appdb"}}
	if _, err := effectiveTargets(&bad, stored); err == nil {
		t.Error("a target naming no sidecar must be refused")
	}
}

// TestEffectiveSpecKeepsTheStoredBlock pins that omission preserves.
//
// Reading an absent sidecar_spec as an empty one disarms a bound rule from a
// form that never mentioned sidecars -- and, because a bound rule with no spec
// is refused, turns an edit to a description into a 422 nobody can act on.
func TestEffectiveSpecKeepsTheStoredBlock(t *testing.T) {
	stored := json.RawMessage(`{"rules":[{"name":"r","type":"operation","operations":["drop"]}]}`)

	if got := (Request{StoredSpec: stored}).EffectiveSpec(); string(got) != string(stored) {
		t.Errorf("an absent spec must keep the stored one, got %s", got)
	}
	sent := json.RawMessage(`{"rules":[]}`)
	if got := (Request{Spec: sent, StoredSpec: stored}).EffectiveSpec(); string(got) != string(sent) {
		t.Errorf("a spec that was sent must replace, got %s", got)
	}
	// An explicit null is how the block is removed on purpose, and it must
	// reach the validator as null rather than as the stored block.
	null := json.RawMessage(`null`)
	if got := (Request{Spec: null, StoredSpec: stored}).EffectiveSpec(); string(got) != "null" {
		t.Errorf("an explicit null must clear the block, got %s", got)
	}
}

// A listener stored before the limit can have a name no binding index holds.
// Its target is refused with its position, never sent to the database.
func TestToModelTargetsRefusesAListenerNameNoIndexHolds(t *testing.T) {
	over := strings.Repeat("l", models.MaxSidecarListenerNameBytes+1)
	_, err := toModelTargets(&[]openapi.SidecarRuleTarget{
		{SidecarID: "sc-1", ListenerName: "appdb"},
		{SidecarID: "sc-1", ListenerName: over},
	})
	if _, ok := err.(malformedTargets); !ok || !strings.Contains(err.Error(), "sidecar target 2: the listener name is 1025 bytes, over 1024") {
		t.Fatalf("want a malformed target naming target 2 and the limit, got %v", err)
	}
	got, err := toModelTargets(&[]openapi.SidecarRuleTarget{
		{SidecarID: "sc-1", ListenerName: strings.Repeat("l", models.MaxSidecarListenerNameBytes)},
	})
	if err != nil || len(got) != 1 {
		t.Fatalf("a name at the limit binds: %v, %d targets", err, len(got))
	}
}
