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

// BuildMySQLEnvVars constructs the AgentConnectionParams env vars map for
// a MySQL connection. The keys must match what parseConnectionEnvVars
// looks up for ConnectionTypeMySQL (HOST, USER, PASS, PORT, DB). libhoop's
// MySQL proxy uses USER/PASS to authenticate against the upstream itself.
func BuildMySQLEnvVars(host, port, user, pass, dbname string) map[string]any {
	return map[string]any{
		"envvar:HOST": b64(host),
		"envvar:USER": b64(user),
		"envvar:PASS": b64(pass),
		"envvar:PORT": b64(port),
		"envvar:DB":   b64(dbname),
	}
}

// SendMySQLTrigger injects a zero-payload MySQLConnectionWrite to bootstrap
// the upstream connection. Exposed for diagnostics; normal tests get this
// for free via DialPipedMySQL.
func SendMySQLTrigger(tr *MockTransport, sessionID, connID string) {
	tr.Inject(&pb.Packet{
		Type: pbagent.MySQLConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(connID),
		},
	})
}

// OpenMySQLSession opens a MySQL session on the agent and blocks until
// SessionOpenOK is observed, returning the generated session ID. It reads
// directly from the transport, so it must be called before StartRecvDemux
// to avoid the demux swallowing the open reply.
func OpenMySQLSession(t *testing.T, tr *MockTransport, mc *MySQLContainer) string {
	t.Helper()
	sessionID := uuid.New().String()
	envVars := BuildMySQLEnvVars(mc.Host, mc.Port, mc.User, mc.Password, mc.Database)
	pkt := BuildSessionOpenPacket(sessionID, string(pb.ConnectionTypeMySQL), envVars)
	tr.Inject(pkt)

	deadline := time.After(10 * time.Second)
	for {
		select {
		case resp := <-tr.RecvCh():
			switch resp.Type {
			case pbclient.SessionOpenOK:
				return sessionID
			case pbclient.SessionClose:
				t.Fatalf("mysql session open failed: %s", string(resp.Payload))
			default:
				continue
			}
		case <-deadline:
			t.Fatalf("timed out waiting for mysql SessionOpenOK")
		}
	}
}
