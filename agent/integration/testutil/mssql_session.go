//go:build integration

package testutil

import (
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// BuildMSSQLEnvVars constructs the AgentConnectionParams env vars map for a
// MSSQL connection. The keys must match what parseConnectionEnvVars looks
// up for ConnectionTypeMSSQL (HOST, USER, PASS, PORT). libhoop's MSSQL
// proxy uses USER/PASS to authenticate against the upstream itself,
// overriding whatever the client sends in its LOGIN7 packet. INSECURE
// controls whether the proxy verifies the upstream server certificate.
func BuildMSSQLEnvVars(host, port, user, pass string) map[string]any {
	return map[string]any{
		"envvar:HOST":     b64(host),
		"envvar:USER":     b64(user),
		"envvar:PASS":     b64(pass),
		"envvar:PORT":     b64(port),
		"envvar:INSECURE": b64("true"),
	}
}

// OpenMSSQLSession opens a MSSQL session on the agent and blocks until
// SessionOpenOK is observed, returning the generated session ID. It reads
// directly from the transport, so it must be called before StartRecvDemux
// to avoid the demux swallowing the open reply.
func OpenMSSQLSession(t *testing.T, tr *MockTransport, mc *MSSQLContainer) string {
	return OpenMSSQLSessionWithGuardRails(t, tr, mc, nil)
}

// OpenMSSQLSessionWithGuardRails is OpenMSSQLSession with guardrail rules
// attached to the connection params, so the agent's MSSQL proxy validates
// statements against them (requires the beta.mssql_native_guardrails flag to be
// enabled in the agent's featureflagstate).
func OpenMSSQLSessionWithGuardRails(t *testing.T, tr *MockTransport, mc *MSSQLContainer, guardRailRules []byte) string {
	t.Helper()
	sessionID := uuid.New().String()
	envVars := BuildMSSQLEnvVars(mc.Host, mc.Port, mc.User, mc.Password)
	pkt := BuildSessionOpenPacketWithGuardRails(sessionID, string(pb.ConnectionTypeMSSQL), envVars, guardRailRules)
	tr.Inject(pkt)

	deadline := time.After(10 * time.Second)
	for {
		select {
		case resp := <-tr.RecvCh():
			switch resp.Type {
			case pbclient.SessionOpenOK:
				return sessionID
			case pbclient.SessionClose:
				t.Fatalf("mssql session open failed: %s", string(resp.Payload))
			default:
				continue
			}
		case <-deadline:
			t.Fatalf("timed out waiting for mssql SessionOpenOK")
		}
	}
}

// SendMSSQLTrigger injects a zero-payload MSSQLConnectionWrite. Exposed for
// diagnostics; normal tests don't need it because the go-mssqldb driver
// speaks first (PRELOGIN), so the first real write creates the proxy.
func SendMSSQLTrigger(tr *MockTransport, sessionID, connID string) {
	tr.Inject(&pb.Packet{
		Type: pbagent.MSSQLConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(connID),
		},
	})
}
