package sidecarbind

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
)

// A gateway has no sidecars. These tests run with no appconfig loaded, which
// reads as gateway mode -- the shipping default, and the state every gateway
// deployment is in.

// TestGatewayModeRefusesTheControlPlaneFields is the guard on the promise that
// this feature does not touch the gateway.
//
// Silently dropping the fields would be worse than refusing them: the rule
// would save, the admin would see it listed, and it would reach nothing.
// Silently STORING them would be worse still -- a column no gateway code
// reads, no gateway page shows and nobody maintains.
func TestGatewayModeRefusesTheControlPlaneFields(t *testing.T) {
	if appconfig.Get().IsControlPlane() {
		t.Fatal("this test needs the default (gateway) app mode")
	}
	spec := json.RawMessage(`{"rules":[{"name":"r","type":"operation","operations":["drop"]}]}`)
	targets := []openapi.SidecarRuleTarget{{SidecarID: "x", ListenerName: "appdb"}}

	for _, tt := range []struct {
		name string
		req  Request
		want int
	}{
		{
			name: "a rule carrying a sidecar spec",
			req:  Request{Kind: services.SidecarRuleGuardrail, Name: "r", Spec: spec},
			want: http.StatusUnprocessableEntity,
		},
		{
			name: "a rule carrying sidecar targets",
			req:  Request{Kind: services.SidecarRuleGuardrail, Name: "r", Targets: &targets},
			want: http.StatusUnprocessableEntity,
		},
		{
			// The ordinary gateway write, which is every write a gateway makes
			// today. It must pass through untouched.
			name: "a rule carrying neither",
			req:  Request{Kind: services.SidecarRuleGuardrail, Name: "r"},
			want: 0,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			answered := Refuse(c, "5c5d1e10-4d7e-4b8f-9a7a-4a0e0f6a9b11", tt.req)

			if tt.want == 0 {
				if answered {
					t.Fatalf("a gateway write mentioning neither field was refused: %s", rec.Body)
				}
				return
			}
			if !answered {
				t.Fatal("want the request answered with a refusal")
			}
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body)
			}
		})
	}
}

// TestGatewayModeWritesNothing pins the other half: even called directly, the
// write and read paths are inert outside a control plane. Neither touches the
// database, so a gateway with no sidecar tables is not a nil dereference
// waiting for a request.
func TestGatewayModeWritesNothing(t *testing.T) {
	targets := []openapi.SidecarRuleTarget{{SidecarID: "x", ListenerName: "appdb"}}
	if err := Persist("5c5d1e10-4d7e-4b8f-9a7a-4a0e0f6a9b11", services.SidecarRuleGuardrail, "r", &targets); err != nil {
		t.Errorf("Persist must be a no-op in a gateway, got %v", err)
	}
	if got := Load("5c5d1e10-4d7e-4b8f-9a7a-4a0e0f6a9b11", services.SidecarRuleGuardrail, "r"); got != nil {
		t.Errorf("Load must answer nothing in a gateway, got %+v", got)
	}
}

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
