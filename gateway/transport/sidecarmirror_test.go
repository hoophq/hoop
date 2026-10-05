package transport

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A mirror has no agent and the gateway has no route to its sidecar. Every
// session on it must stop at the gate with a reason the client can act on,
// not reach the agent request with an empty agent ID.
func TestRefuseSidecarMirror(t *testing.T) {
	if err := refuseSidecarMirror("pg-prod", ""); err != nil {
		t.Errorf("a connection with no sidecar must open, got %v", err)
	}
	err := refuseSidecarMirror("pay-appdb", "9b2a8b4e-5c0e-4a51-9b7a-1f3e2d4c5b6a")
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
	if !strings.Contains(err.Error(), `"pay-appdb"`) || !strings.Contains(err.Error(), "sidecar listener") {
		t.Errorf("the reason must name the connection and the way in, got %q", err)
	}
}
