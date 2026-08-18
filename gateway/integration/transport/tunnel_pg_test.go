//go:build integration

package transport

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/google/uuid"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// TestTunnelRawTCPRejectsPlaceholderCredentials pins the defect behind DEP-142.
//
// The tunnel (tunnel/client/pipe.go) relays every TCP-style connection —
// postgres included — as pbagent.TCPConnectionWrite. That packet family lands
// on the agent's raw relay (agent/controller/tcp.go), which dials the upstream
// and copies bytes verbatim: no credential injection. So a client presenting
// the documented noop/noop placeholder authenticates against the real database
// as the literal user "noop" and is rejected.
//
// This is what makes `hsh tunnel ls` unable to advertise fixed local
// credentials today. The companion test below asserts the protocol packet
// family does inject credentials, which is the behaviour the tunnel must adopt.
func TestTunnelRawTCPRejectsPlaceholderCredentials(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			connName := uniqueName("tunraw")
			agentID, dsn := createAgent(t, uniqueName("agent"))
			createPGConnection(t, connName, agentID)
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
			r := newPGReader(cli)
			defer r.close()

			sid := uuid.NewString()
			if err := cli.Send(&pb.Packet{
				Type: pbagent.SessionOpen,
				Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(sid)},
			}); err != nil {
				t.Fatalf("send SessionOpen: %v", err)
			}
			r.waitFor(t, pbclient.SessionOpenOK, 20*time.Second)

			// Exactly what tunnel/client/pipe.go does today: an empty
			// TCPConnectionWrite carrying SpecTCPServerConnectKey to make the
			// agent dial upstream, then raw protocol bytes on the same family.
			const connID = "1"
			if err := cli.Send(&pb.Packet{
				Type: pbagent.TCPConnectionWrite,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID:    []byte(sid),
					pb.SpecClientConnectionID:  []byte(connID),
					pb.SpecTCPServerConnectKey: nil,
				},
			}); err != nil {
				t.Fatalf("send TCP open: %v", err)
			}

			// A native client speaking to the tunnel presents the placeholder
			// credentials the docs advertise. The raw relay is a verbatim byte
			// pipe, so the SSL negotiation must be driven properly: send
			// SSLRequest, wait for the single-byte reply, then the startup
			// message. Batching them makes postgres reject the stream as
			// "unencrypted data after SSL request" before it ever looks at the
			// role, which would prove nothing about credentials.
			rawWrite := func(payload []byte) {
				t.Helper()
				if err := cli.Send(&pb.Packet{
					Type:    pbagent.TCPConnectionWrite,
					Payload: payload,
					Spec: map[string][]byte{
						pb.SpecGatewaySessionID:   []byte(sid),
						pb.SpecClientConnectionID: []byte(connID),
					},
				}); err != nil {
					t.Fatalf("raw write: %v", err)
				}
			}
			rawWrite(pgSSLRequest())
			r.readRawSSLReply(t, 20*time.Second)
			rawWrite(pgStartupMessage("noop", gw.Postgres.Database))

			// The raw relay hands the client the backend's own authentication
			// challenge, so the client itself must satisfy it. Placeholder
			// credentials cannot: hoop's stored secrets never enter this path.
			got := r.readRawUntilAuthOutcome(t, 20*time.Second)
			switch {
			case got.ready:
				t.Fatal("raw TCP relay reached ReadyForQuery for role \"noop\"; " +
					"the upstream would have to actually own that role")
			case got.authOK:
				t.Fatal("raw TCP relay authenticated role \"noop\" upstream; " +
					"credential injection would be unnecessary")
			case got.authType != 0:
				t.Logf("raw TCP relay forwarded the upstream auth challenge (type %d) to the client: "+
					"placeholder credentials cannot satisfy it", got.authType)
			case got.errText != "":
				t.Logf("raw TCP relay rejected placeholder credentials: %s", got.errText)
			default:
				t.Fatal("raw relay produced no authentication outcome")
			}
		})
	}
}

// TestProtocolFamilyInjectsCredentials is the positive control: the same
// connection, same placeholder credentials, but relayed on the protocol packet
// family the `hoop connect` client uses. The agent runs the libhoop PostgreSQL
// proxy, which terminates client auth locally and re-authenticates upstream
// with the connection's stored secrets — so noop/noop reaches ReadyForQuery.
//
// This is the contract `hsh tunnel ls` needs in order to advertise fixed local
// credentials.
func TestProtocolFamilyInjectsCredentials(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			connName := uniqueName("tunproto")
			agentID, dsn := createAgent(t, uniqueName("agent"))
			createPGConnection(t, connName, agentID)
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
			r := newPGReader(cli)
			defer r.close()

			sid := uuid.NewString()
			if err := cli.Send(&pb.Packet{
				Type: pbagent.SessionOpen,
				Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(sid)},
			}); err != nil {
				t.Fatalf("send SessionOpen: %v", err)
			}
			r.waitFor(t, pbclient.SessionOpenOK, 20*time.Second)

			const connID = "1"
			handshake := append(pgSSLRequest(), pgStartupMessage("noop", gw.Postgres.Database)...)
			if err := cli.Send(pgWritePacket(sid, connID, handshake)); err != nil {
				t.Fatalf("send PG handshake: %v", err)
			}
			r.readUntilReady(t, 20*time.Second)

			if err := cli.Send(pgWritePacket(sid, connID, pgSimpleQuery("SELECT 1"))); err != nil {
				t.Fatalf("send PG query: %v", err)
			}
			rows := r.readUntilReady(t, 20*time.Second)
			if !containsValue(rows, "1") {
				t.Fatalf("SELECT 1 over the protocol family with noop/noop: got %v", rowsToStrings(rows))
			}
		})
	}
}

// rawRelayOutcome is what a PG startup exchange produced when carried over the
// raw TCP packet family. Exactly one field is meaningful per run.
type rawRelayOutcome struct {
	// ready means the backend reached ReadyForQuery.
	ready bool
	// authOK means the backend accepted the client's identity outright
	// (AuthenticationOk, subtype 0) — i.e. it is trust-authenticated.
	authOK bool
	// authType is the non-zero AuthenticationRequest subtype the backend
	// demanded from the client (10 = SASL/SCRAM, 5 = MD5, 3 = cleartext).
	authType uint32
	// errText is the backend's ErrorResponse payload, when it rejected the
	// startup outright.
	errText string
}

// readRawUntilAuthOutcome consumes pbclient.TCPConnectionWrite packets — the
// family the raw relay answers on — until the backend's startup exchange
// resolves one way or another.
//
// Unlike readUntilReady it treats an authentication challenge or an
// ErrorResponse as the expected result rather than a fatal: the whole point of
// this path is that hoop's stored credentials never participate, so the client
// is left facing the database's own auth demand.
//
// The relay copies bytes verbatim, so writes carry no message boundaries;
// payloads are concatenated before parsing and re-parsed on each arrival.
func (r *pgReader) readRawUntilAuthOutcome(t *testing.T, timeout time.Duration) rawRelayOutcome {
	t.Helper()
	var buf []byte
	deadline := time.After(timeout)
	for {
		select {
		case pkt := <-r.pkts:
			switch pkt.Type {
			case pbclient.SessionClose:
				// A dial or relay failure surfaces as a session close rather
				// than a PG ErrorResponse; either way auth did not succeed.
				return rawRelayOutcome{errText: string(pkt.Payload)}
			case pbclient.TCPConnectionClose:
				return rawRelayOutcome{errText: "upstream closed the connection"}
			case pbclient.TCPConnectionWrite:
				buf = append(buf, pkt.Payload...)
			default:
				continue
			}
			for _, m := range parsePGMessages(buf) {
				switch m.Type {
				case 'E':
					return rawRelayOutcome{errText: string(m.Payload)}
				case 'Z':
					return rawRelayOutcome{ready: true}
				case 'R':
					if len(m.Payload) < 4 {
						continue
					}
					subtype := binary.BigEndian.Uint32(m.Payload[:4])
					if subtype == 0 {
						return rawRelayOutcome{authOK: true}
					}
					return rawRelayOutcome{authType: subtype}
				}
			}
		case err := <-r.errc:
			t.Fatalf("stream error during raw relay exchange: %v", err)
		case <-deadline:
			t.Fatalf("timed out during raw relay exchange after %v", timeout)
		}
	}
}

// readRawSSLReply consumes the single-byte SSLRequest answer ('N' when the
// backend declines TLS, 'S' when it offers it) that postgres sends before any
// length-prefixed message. It exists because the raw relay is a verbatim byte
// pipe with no protocol awareness, so the test must observe the negotiation
// step a real client would.
func (r *pgReader) readRawSSLReply(t *testing.T, timeout time.Duration) byte {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case pkt := <-r.pkts:
			switch pkt.Type {
			case pbclient.SessionClose:
				t.Fatalf("session closed awaiting the SSL reply: %s", string(pkt.Payload))
			case pbclient.TCPConnectionWrite:
				if len(pkt.Payload) == 0 {
					continue
				}
				if b := pkt.Payload[0]; b == 'N' || b == 'S' {
					return b
				}
				t.Fatalf("expected an SSL negotiation reply, got % X", pkt.Payload)
			}
		case err := <-r.errc:
			t.Fatalf("stream error awaiting the SSL reply: %v", err)
		case <-deadline:
			t.Fatalf("timed out awaiting the SSL reply after %v", timeout)
		}
	}
}
