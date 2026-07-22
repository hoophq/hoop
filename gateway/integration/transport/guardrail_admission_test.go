//go:build integration

package transport

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// NOTE: This file no longer contains native-MSSQL guardrail admission tests.
// Under the DEP-74 delegate model the gateway performs NO guardrail admission
// or agent-capability gating for MSSQL: libhoop is the sole authority and fails
// closed at proxy construction when it cannot enforce the rules. The gateway
// only chooses whether to SHIP rules (see dropNativeMSSQLGuardRails in
// gateway/transport/client.go, unit-tested in agent_guardrails_test.go). The
// remaining test below covers the one gateway-side admission invariant that
// still exists: a guarded connection opens without a DLP provider configured.

// TestGuardedConnectionOpensWithoutProvider verifies the gateway admits a
// session on a guarded connection even when no Presidio provider is configured.
// Guardrails are enforced by the agent's built-in pattern-matching engine (see
// gateway/guardrails), not by a DLP provider, so the earlier DEP-48 Presidio
// gate was removed — it broke deployments that rely on that engine.
//
// It binds an explicit guardrail rule to a supported (postgres) connection.
// The admission decision is gateway-side; SessionOpenOK is sent by the agent
// before any protocol proxy runs, so this needs no enterprise libhoop and runs
// on the OSS stub. A FailedPrecondition here (the old behavior) would mean the
// Presidio gate came back.
func TestGuardedConnectionOpensWithoutProvider(t *testing.T) {
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

	// The guarded session is admitted: the agent replies SessionOpenOK. If the
	// Presidio gate were still present the gateway would instead terminate the
	// stream with FailedPrecondition, which recvUntil surfaces as an error.
	if _, err := recvUntil(cli, 15*time.Second, pbclient.SessionOpenOK); err != nil {
		t.Fatalf("guarded connection was not admitted without a Presidio provider: %v", err)
	}
}
