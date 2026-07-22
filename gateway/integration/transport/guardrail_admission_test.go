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
)

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

// openMSSQLSession dials the given connection and sends a SessionOpen, returning
// the client so the caller can assert on admission.
func openMSSQLSession(t *testing.T, c Connector, connName string) pb.ClientTransport {
	t.Helper()
	cli, err := c.DialClient(context.Background(), ClientDialConfig{
		Token:          adminToken(t),
		ConnectionName: connName,
		Verb:           pb.ClientVerbConnect,
	})
	if err != nil {
		t.Fatalf("DialClient: %v", err)
	}
	if err := cli.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(uuid.NewString())},
	}); err != nil {
		cli.Close()
		t.Fatalf("send SessionOpen: %v", err)
	}
	return cli
}

// TestMSSQLNativeGuarded_OutputRulesRefused verifies a native MSSQL session on a
// connection that has output guardrail rules is refused fail-closed, because the
// TDS proxy enforces input rules only.
func TestMSSQLNativeGuarded_OutputRulesRefused(t *testing.T) {
	c := transports()[0]

	connName := uniqueName("mssql-out")
	agentID, dsn := createAgent(t, uniqueName("agent"))
	postMSSQLConnection(t, connName, agentID)
	clearOrgGuardrails(t)
	createOutputGuardrailForConnection(t, uniqueName("gr-out"), connectionID(t, connName))
	startAgent(t, c, dsn)
	waitConnectionOnline(t, connName)

	cli := openMSSQLSession(t, c, connName)
	defer cli.Close()
	// Output rules cannot be enforced natively, so admission is refused.
	_, err := recvUntil(cli, 10*time.Second, pbclient.SessionOpenOK)
	if err == nil {
		t.Fatal("native MSSQL with output guardrails must be refused, but the session was admitted")
	}
	if !strings.Contains(err.Error(), "output guardrail rules") {
		t.Errorf("expected an output-rules refusal, got: %v", err)
	}
}

// TestMSSQLNativeGuarded_AgentWithoutCapabilityRefused verifies a native MSSQL
// session is refused fail-closed when the selected agent does not advertise the
// MSSQL guardrails capability, standing in for an older agent that would run the
// session unguarded — the refusal is what makes the default-on flag safe.
func TestMSSQLNativeGuarded_AgentWithoutCapabilityRefused(t *testing.T) {
	c := transports()[0]

	connName := uniqueName("mssql-cap")
	agentID, dsn := createAgent(t, uniqueName("agent"))
	postMSSQLConnection(t, connName, agentID)
	clearOrgGuardrails(t)
	createGuardrailForConnection(t, uniqueName("gr-in"), connectionID(t, connName)) // input-only
	startAgentWithoutCapabilities(t, c, dsn)
	waitConnectionOnline(t, connName)

	cli := openMSSQLSession(t, c, connName)
	defer cli.Close()
	_, err := recvUntil(cli, 10*time.Second, pbclient.SessionOpenOK)
	if err == nil {
		t.Fatal("native MSSQL on an agent without the guardrails capability must be refused")
	}
	if !strings.Contains(err.Error(), "does not support native MSSQL guardrail enforcement") {
		t.Errorf("expected a capability refusal, got: %v", err)
	}
}

// TestMSSQLNativeGuarded_CapableAgentAdmitted verifies the happy path: a native
// MSSQL session on an input-only guarded connection, served by an agent that
// advertises the capability, PASSES gateway admission. It is not refused at
// session-open; it proceeds and fails only later on the upstream dial (there is
// no real SQL Server here), proving it cleared the guardrail admission gate.
// This guards against a routing/metadata regression silently failing every
// guarded session closed.
func TestMSSQLNativeGuarded_CapableAgentAdmitted(t *testing.T) {
	c := transports()[0]

	connName := uniqueName("mssql-ok")
	agentID, dsn := createAgent(t, uniqueName("agent"))
	postMSSQLConnection(t, connName, agentID)
	clearOrgGuardrails(t)
	createGuardrailForConnection(t, uniqueName("gr-in"), connectionID(t, connName)) // input-only
	startAgent(t, c, dsn)                                                           // advertises the capability
	waitConnectionOnline(t, connName)

	cli := openMSSQLSession(t, c, connName)
	defer cli.Close()
	_, err := recvUntil(cli, 15*time.Second, pbclient.SessionOpenOK)
	// Either SessionOpenOK arrived, or the session got past admission and failed
	// on the upstream dial. Both mean admission succeeded. A guardrail/capability
	// refusal (FailedPrecondition) must NOT appear.
	if err != nil {
		if strings.Contains(err.Error(), "guardrail") ||
			strings.Contains(err.Error(), "does not support") ||
			strings.Contains(err.Error(), "output guardrail rules") {
			t.Fatalf("capable agent must pass admission, but was refused: %v", err)
		}
	}
}
