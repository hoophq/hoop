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

// BuildMongoEnvVars constructs the AgentConnectionParams env vars map for a
// MongoDB connection. parseConnectionEnvVars for ConnectionTypeMongoDB
// reads CONNECTION_STRING and parses it with the official driver's
// connstring validator, so the value must be a full mongodb:// URI with
// the real upstream credentials. libhoop's proxy authenticates against the
// upstream itself using these credentials.
func BuildMongoEnvVars(connectionString string) map[string]any {
	return map[string]any{
		"envvar:CONNECTION_STRING": b64(connectionString),
	}
}

// OpenMongoSession opens a MongoDB session on the agent and blocks until
// SessionOpenOK is observed, returning the generated session ID. It reads
// directly from the transport, so it must be called before StartRecvDemux
// to avoid the demux swallowing the open reply.
func OpenMongoSession(t *testing.T, tr *MockTransport, mc *MongoContainer) string {
	t.Helper()
	sessionID := uuid.New().String()
	envVars := BuildMongoEnvVars(mc.UpstreamConnString())
	pkt := BuildSessionOpenPacket(sessionID, string(pb.ConnectionTypeMongoDB), envVars)
	tr.Inject(pkt)

	deadline := time.After(10 * time.Second)
	for {
		select {
		case resp := <-tr.RecvCh():
			switch resp.Type {
			case pbclient.SessionOpenOK:
				return sessionID
			case pbclient.SessionClose:
				t.Fatalf("mongodb session open failed: %s", string(resp.Payload))
			default:
				continue
			}
		case <-deadline:
			t.Fatalf("timed out waiting for mongodb SessionOpenOK")
		}
	}
}

// OpenMongoSessionWithConnString is like OpenMongoSession but lets the
// caller supply an arbitrary connection string (e.g. with wrong
// credentials) to exercise failure paths.
func OpenMongoSessionWithConnString(t *testing.T, tr *MockTransport, connectionString string) string {
	t.Helper()
	sessionID := uuid.New().String()
	envVars := BuildMongoEnvVars(connectionString)
	pkt := BuildSessionOpenPacket(sessionID, string(pb.ConnectionTypeMongoDB), envVars)
	tr.Inject(pkt)

	deadline := time.After(10 * time.Second)
	for {
		select {
		case resp := <-tr.RecvCh():
			switch resp.Type {
			case pbclient.SessionOpenOK:
				return sessionID
			case pbclient.SessionClose:
				t.Fatalf("mongodb session open failed: %s", string(resp.Payload))
			default:
				continue
			}
		case <-deadline:
			t.Fatalf("timed out waiting for mongodb SessionOpenOK")
		}
	}
}

// SendMongoTrigger injects a zero-payload MongoDBConnectionWrite. Exposed
// for diagnostics; normal tests don't need it because the mongo driver
// speaks first (legacy OP_QUERY hello), so the first real write creates
// the proxy.
func SendMongoTrigger(tr *MockTransport, sessionID, connID string) {
	tr.Inject(&pb.Packet{
		Type: pbagent.MongoDBConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(connID),
		},
	})
}
