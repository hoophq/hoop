package sidecarbind

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
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
