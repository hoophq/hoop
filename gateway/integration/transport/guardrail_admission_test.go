//go:build integration

package transport

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestGuardedConnectionRefusedWithoutProvider is the guard for the guardrails
// fix in getGuardRailsRulesForConnection: the fix makes a connection with ZERO
// rules open normally (proven by TestPGProtocolRoundTrip), and this test proves
// the other, security-critical half — a connection that DOES have real
// guardrail rules is still fail-closed at session-open when no Presidio provider
// is configured (#1573 / DEP-48).
//
// It binds an explicit guardrail rule to the connection (rather than relying on
// the shared org's default security-pack, which another test may have cleared),
// so it is order-independent. The refusal happens gateway-side before the agent
// proxy runs, so this test needs no enterprise libhoop and runs on the OSS stub.
func TestGuardedConnectionRefusedWithoutProvider(t *testing.T) {
	c := transports()[0] // gateway-side admission; wire-agnostic, gRPC suffices

	connName := uniqueName("guarded")
	agentID, dsn := createAgent(t, uniqueName("agent"))
	postPGConnection(t, connName, agentID)
	createGuardrailForConnection(t, uniqueName("gr"), connectionID(t, connName))
	startAgent(t, c, dsn)
	waitConnectionOnline(t, connName)

	cli, err := c.DialClient(context.Background(), ClientDialConfig{
		Token:          adminToken(t),
		ConnectionName: connName,
		Verb:           pb.ClientVerbConnect,
	})
	if err != nil {
		t.Fatalf("DialClient: %v", err)
	}
	defer cli.Close()

	if err := cli.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(uuid.NewString())},
	}); err != nil {
		t.Fatalf("send SessionOpen: %v", err)
	}

	// The guarded session must be refused at open time (DEP-48): the gateway
	// terminates the Connect stream with FailedPrecondition before the agent
	// ever opens the upstream. Receiving SessionOpenOK instead would mean the
	// guardrails fix regressed and unguarded a guarded connection.
	pkt, err := recvUntil(cli, 15*time.Second, pbclient.SessionOpenOK)
	if err == nil {
		t.Fatalf("guarded connection opened (got %s) without a Presidio provider; DEP-48 protection regressed", pkt.Type)
	}
	if code := status.Code(err); code != codes.FailedPrecondition {
		t.Fatalf("refusal error code = %v, want FailedPrecondition (err=%v)", code, err)
	}
	if !strings.Contains(err.Error(), "Presidio") {
		t.Fatalf("expected a Presidio/guardrail refusal, got: %v", err)
	}
}
