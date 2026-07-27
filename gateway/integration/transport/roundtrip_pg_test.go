//go:build integration

package transport

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/integration/testutil"
)

// TestPGProtocolRoundTrip is the end-to-end protocol round-trip: a real agent
// controller connects over the transport, a client opens a session and runs
// `SELECT 1` through the agent's transparent PostgreSQL proxy to a real
// database, and the row comes back over the same wire. It exercises the full
// interceptor chain (sessionuuid → auth → tracing → accessrequest), the plugin
// chain (PluginExecOnReceive), bidirectional packet streaming, and one
// protocol round-trip — the DEP-39 acceptance path.
func TestPGProtocolRoundTrip(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			connName := uniqueName("pgconn")
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

			// pgReader owns cli's lifecycle (see its doc); do not also close cli.
			r := newPGReader(cli)
			defer r.close()

			// Open the session. The gateway assigns the real SID (overwriting
			// whatever we send) and enriches the packet with the connection's
			// credentials before forwarding to the agent.
			sid := uuid.NewString()
			if err := cli.Send(&pb.Packet{
				Type: pbagent.SessionOpen,
				Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(sid)},
			}); err != nil {
				t.Fatalf("send SessionOpen: %v", err)
			}
			r.waitFor(t, pbclient.SessionOpenOK, 20*time.Second)

			// PG handshake: SSLRequest + StartupMessage in one write. The agent's
			// proxy authenticates upstream with the connection's stored creds and
			// relays ReadyForQuery back.
			const connID = "1"
			handshake := append(pgSSLRequest(), pgStartupMessage(gw.Postgres.User, gw.Postgres.Database)...)
			if err := cli.Send(pgWritePacket(sid, connID, handshake)); err != nil {
				t.Fatalf("send PG handshake: %v", err)
			}
			r.readUntilReady(t, 20*time.Second)

			// Run the query and read the result set.
			if err := cli.Send(pgWritePacket(sid, connID, pgSimpleQuery("SELECT 1"))); err != nil {
				t.Fatalf("send PG query: %v", err)
			}
			rows := r.readUntilReady(t, 20*time.Second)

			if !containsValue(rows, "1") {
				t.Fatalf("SELECT 1 round-trip: expected a row with value \"1\", got %v", rowsToStrings(rows))
			}

			// The whole session only works because the sessionuuid interceptor
			// injected a SID that keyed the proxy stream and plugin context; the
			// audit plugin then persisted a session under it. Assert that record
			// exists as a direct check of both.
			assertSessionRecorded(t)
		})
	}
}

// assertSessionRecorded verifies at least one session was persisted with a
// valid UUID id — evidence the sessionuuid interceptor injected an id and the
// audit plugin wrote the session row.
func assertSessionRecorded(t *testing.T) {
	t.Helper()
	resp := gw.HTTP.Get(t, "/sessions", adminToken(t))
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, 200)

	var list openapi.SessionList
	testutil.DecodeJSON(t, resp, &list)
	if list.Total < 1 || len(list.Items) == 0 {
		t.Fatal("no sessions recorded; sessionuuid/audit did not persist the client session")
	}
	if _, err := uuid.Parse(list.Items[0].ID); err != nil {
		t.Fatalf("recorded session id %q is not a valid UUID: %v", list.Items[0].ID, err)
	}
}

// --- PG stream reader ----------------------------------------------------

// pgReader owns a single background reader goroutine over a client transport,
// feeding received packets to callers via a channel. It exists because the
// round-trip reads the stream in several phases (SessionOpenOK, ReadyForQuery,
// query result) and a single reader avoids racing multiple goroutines on one
// stream. The reader owns the transport's lifecycle: close() both signals the
// goroutine to stop and closes the transport, which unblocks the in-flight
// Recv so the goroutine always exits (no leak). Callers therefore defer only
// r.close(), not cli.Close().
type pgReader struct {
	cli       pb.ClientTransport
	pkts      chan *pb.Packet
	errc      chan error
	stop      chan struct{}
	closeOnce sync.Once
}

func newPGReader(cli pb.ClientTransport) *pgReader {
	r := &pgReader{
		cli:  cli,
		pkts: make(chan *pb.Packet, 64),
		errc: make(chan error, 1),
		stop: make(chan struct{}),
	}
	go func() {
		for {
			pkt, err := r.cli.Recv()
			if err != nil {
				select {
				case r.errc <- err:
				case <-r.stop:
				}
				return
			}
			select {
			case r.pkts <- pkt:
			case <-r.stop:
				return
			}
		}
	}()
	return r
}

func (r *pgReader) close() {
	r.closeOnce.Do(func() {
		close(r.stop)
		_, _ = r.cli.Close()
	})
}

// ossLibhoopMarker is the error the open-source _libhoop proxy stub returns
// for every protocol. The real protocol proxy lives in the enterprise libhoop
// (checked out in the integration-test CI job); when running against the stub
// there is no way to complete a real PG exchange, so the round-trip skips
// rather than fails — the same OSS/enterprise split the agent suite relies on.
const ossLibhoopMarker = "missing protocol hoop library"

// waitFor blocks until a packet of the given type arrives.
func (r *pgReader) waitFor(t *testing.T, packetType string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case pkt := <-r.pkts:
			if pkt.Type == packetType {
				return
			}
			if pkt.Type == pbclient.SessionClose {
				if strings.Contains(string(pkt.Payload), ossLibhoopMarker) {
					t.Skip("PG round-trip requires the enterprise libhoop protocol proxy; skipping on the OSS stub")
				}
				t.Fatalf("unexpected SessionClose waiting for %s: %s", packetType, string(pkt.Payload))
			}
		case err := <-r.errc:
			t.Fatalf("stream error waiting for %s: %v", packetType, err)
		case <-deadline:
			t.Fatalf("timed out waiting for %s after %v", packetType, timeout)
		}
	}
}

// readUntilReady consumes PGConnectionWrite packets until ReadyForQuery ('Z'),
// returning the first-column value of every DataRow seen along the way.
func (r *pgReader) readUntilReady(t *testing.T, timeout time.Duration) [][]byte {
	t.Helper()
	var rows [][]byte
	deadline := time.After(timeout)
	for {
		select {
		case pkt := <-r.pkts:
			if pkt.Type == pbclient.SessionClose {
				if strings.Contains(string(pkt.Payload), ossLibhoopMarker) {
					t.Skip("PG round-trip requires the enterprise libhoop protocol proxy; skipping on the OSS stub")
				}
				t.Fatalf("unexpected SessionClose during PG exchange: %s", string(pkt.Payload))
			}
			if pkt.Type != pbclient.PGConnectionWrite {
				continue
			}
			for _, m := range parsePGMessages(pkt.Payload) {
				switch m.Type {
				case 'D':
					if vals := dataRowValues(m); len(vals) > 0 {
						rows = append(rows, vals[0])
					}
				case 'E':
					t.Fatalf("postgres ErrorResponse during PG exchange: %q", string(m.Payload))
				case 'Z':
					return rows
				}
			}
		case err := <-r.errc:
			t.Fatalf("stream error during PG exchange: %v", err)
		case <-deadline:
			t.Fatalf("timed out during PG exchange after %v", timeout)
		}
	}
}

func containsValue(rows [][]byte, want string) bool {
	for _, r := range rows {
		if string(r) == want {
			return true
		}
	}
	return false
}

func rowsToStrings(rows [][]byte) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = string(r)
	}
	return out
}
