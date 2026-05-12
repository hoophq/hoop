//go:build integration

package testutil

import (
	sshtypes "libhoop/proxy/ssh/types"
	"time"

	"github.com/google/uuid"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// BuildSSHEnvVars constructs the AgentConnectionParams env vars map for an
// SSH connection. The keys must match what parseConnectionEnvVars looks up
// for ConnectionTypeSSH (HOST, USER, PASS, PORT, AUTHORIZED_SERVER_KEYS).
func BuildSSHEnvVars(host, port, user, pass string) map[string]any {
	return map[string]any{
		"envvar:HOST": b64(host),
		"envvar:USER": b64(user),
		"envvar:PASS": b64(pass),
		"envvar:PORT": b64(port),
	}
}

// OpenSSHSession opens an SSH session on the agent and returns the
// generated session ID after SessionOpenOK is observed. Fails the test if
// the agent rejects the open or doesn't reply within 10 seconds.
func OpenSSHSession(t T, tr *MockTransport, host, port, user, pass string) string {
	t.Helper()
	sessionID := uuid.New().String()
	envVars := BuildSSHEnvVars(host, port, user, pass)
	pkt := BuildSessionOpenPacket(sessionID, string(pb.ConnectionTypeSSH), envVars)
	tr.Inject(pkt)

	deadline := time.After(10 * time.Second)
	for {
		select {
		case resp := <-tr.RecvCh():
			switch resp.Type {
			case pbclient.SessionOpenOK:
				return sessionID
			case pbclient.SessionClose:
				t.Fatalf("ssh session open failed: %s", string(resp.Payload))
			default:
				continue
			}
		case <-deadline:
			t.Fatalf("timed out waiting for ssh SessionOpenOK")
		}
	}
}

// SendSSHWrite injects an SSHConnectionWrite packet carrying the given
// payload for (sessionID, connID). The payload is expected to be an
// encoded sshtypes packet that libhoop's SSH proxy will dispatch on
// upstream — anything else will fail libhoop's parser and trigger a
// session close.
func SendSSHWrite(t T, tr *MockTransport, sessionID, connID string, payload []byte) {
	t.Helper()
	pkt := &pb.Packet{
		Type: pbagent.SSHConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(connID),
		},
		Payload: payload,
	}
	tr.Inject(pkt)
}

// SSHOpenChannelPayload builds an encoded sshtypes.OpenChannel packet
// requesting a "session" channel on the given channel ID. This is the
// canonical "first packet" in a hoop SSH flow — the client side asks the
// agent to open a session channel against the upstream sshd, after which
// data and exec/pty requests can flow through it.
//
// Using "session" (not "direct-tcpip") avoids libhoop's checkTCPLiveness
// path, which would do its own 2-second blocking dial inside libhoop's
// proxy goroutine and complicate timing assertions in concurrency tests.
func SSHOpenChannelPayload(channelID uint16) []byte {
	return (sshtypes.OpenChannel{
		ChannelID:        channelID,
		ChannelType:      "session",
		ChannelExtraData: nil,
	}).Encode()
}

// SSHDataPayload builds an encoded sshtypes.Data packet for the given
// channel. Used by tests that need to drive multiple packets through an
// already-open channel — for example, the SessionClose race test where
// we want handlers in flight when SessionClose lands.
func SSHDataPayload(channelID uint16, data []byte) []byte {
	return (sshtypes.Data{
		ChannelID: channelID,
		Payload:   data,
	}).Encode()
}


