package client

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// buildStartupPacket builds a minimal Postgres v3 startup packet:
// [int32 length][int32 protocol=196608]["user\0value\0database\0value\0\0"].
func buildStartupPacket(params map[string]string) []byte {
	var body []byte
	proto := make([]byte, 4)
	binary.BigEndian.PutUint32(proto, 196608) // protocol 3.0
	body = append(body, proto...)
	for k, v := range params {
		body = append(body, []byte(k)...)
		body = append(body, 0x00)
		body = append(body, []byte(v)...)
		body = append(body, 0x00)
	}
	body = append(body, 0x00) // terminating null

	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(4+len(body)))
	copy(out[4:], body)
	return out
}

// buildSSLRequest builds the 8-byte SSLRequest packet.
func buildSSLRequest() []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint32(out[:4], 8)
	binary.BigEndian.PutUint32(out[4:], pgtypes.ClientSSLRequestMessage)
	return out
}

// buildSimpleQuery builds a 'Q' simple-query message.
func buildSimpleQuery(sql string) []byte {
	body := append([]byte(sql), 0x00)
	out := make([]byte, 1+4+len(body))
	out[0] = byte(pgtypes.ClientSimpleQuery)
	binary.BigEndian.PutUint32(out[1:5], uint32(4+len(body)))
	copy(out[5:], body)
	return out
}

// TestRunPipe_Postgres_ProtocolAware verifies that a Postgres connection is
// driven through pbagent.PGConnectionWrite (so the agent injects the real
// upstream credentials), NOT the raw TCP pump. The user-supplied credentials
// in the startup packet are forwarded verbatim to the agent — which discards
// them — so the test uses "hoop"/"hoop" to document that any value works.
func TestRunPipe_Postgres_ProtocolAware(t *testing.T) {
	ft := newFakeTransport()
	const sessionID = "sess-pg"

	startup := buildStartupPacket(map[string]string{"user": "hoop", "database": "hoop"})
	query := buildSimpleQuery("SELECT 1")

	// local delivers: startup packet, then a simple query, then blocks
	// until close (simulating an idle psql session).
	local := newFakeLocal(append(append([]byte{}, startup...), query...))

	const wantServerBytes = "server-row-data"

	go func() {
		// Step 1: pipe sends SessionOpen.
		pkts := waitForSent(t, ft, 1, 2*time.Second)
		if pb.PacketType(pkts[0].Type) != pbagent.SessionOpen {
			t.Errorf("packet[0]: want SessionOpen, got %s", pkts[0].Type)
		}

		// Step 2: respond SessionOpenOK for postgres.
		ft.push(&pb.Packet{
			Type: pbclient.SessionOpenOK,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID: []byte(sessionID),
				pb.SpecConnectionType:   []byte(pb.ConnectionTypePostgres.String()),
			},
		})

		// Step 3: wait for the startup packet + the query, both as
		// PGConnectionWrite (NOT TCPConnectionWrite).
		var sawStartup, sawQuery bool
		deadline := time.After(2 * time.Second)
		for {
			for _, p := range ft.sentPackets() {
				if pb.PacketType(p.Type) == pbagent.TCPConnectionWrite {
					t.Errorf("postgres path must not emit TCPConnectionWrite, got one")
				}
				if pb.PacketType(p.Type) == pbagent.PGConnectionWrite {
					switch {
					case len(p.Payload) >= 8 && binary.BigEndian.Uint32(p.Payload[4:8]) == 196608:
						sawStartup = true
					case len(p.Payload) > 0 && p.Payload[0] == byte(pgtypes.ClientSimpleQuery):
						sawQuery = true
					}
				}
			}
			if sawStartup && sawQuery {
				break
			}
			select {
			case <-deadline:
				t.Errorf("timeout: sawStartup=%v sawQuery=%v", sawStartup, sawQuery)
				return
			case <-time.After(5 * time.Millisecond):
			}
		}

		// Step 4: server sends a PGConnectionWrite response, then closes.
		ft.push(&pb.Packet{
			Type: pbclient.PGConnectionWrite,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID:   []byte(sessionID),
				pb.SpecClientConnectionID: []byte(connectionIDOnPipe),
			},
			Payload: []byte(wantServerBytes),
		})
		ft.push(&pb.Packet{
			Type: pbclient.SessionClose,
			Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
		})
	}()

	err := runPipe(context.Background(), ft, local, PipeOptions{
		ConnectionName:     "pg-prod",
		SessionOpenTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("runPipe returned error: %v", err)
	}

	if got := string(local.writtenBytes()); got != wantServerBytes {
		t.Errorf("local writes: want %q, got %q", wantServerBytes, got)
	}

	// Assert the FIRST data packet after SessionOpen is the startup packet
	// sent as PGConnectionWrite.
	pkts := ft.sentPackets()
	if len(pkts) < 2 {
		t.Fatalf("expected >=2 packets, got %d", len(pkts))
	}
	if pb.PacketType(pkts[1].Type) != pbagent.PGConnectionWrite {
		t.Errorf("packet[1]: want PGConnectionWrite, got %s", pkts[1].Type)
	}
	if string(pkts[1].Spec[pb.SpecGatewaySessionID]) != sessionID {
		t.Errorf("packet[1] sessionID: want %q, got %q", sessionID, pkts[1].Spec[pb.SpecGatewaySessionID])
	}
}

// TestRunPipe_Postgres_SSLRequestDeclined verifies the pump replies 'N' to a
// client SSLRequest and then forwards the subsequent plaintext startup packet.
func TestRunPipe_Postgres_SSLRequestDeclined(t *testing.T) {
	ft := newFakeTransport()
	const sessionID = "sess-ssl"

	ssl := buildSSLRequest()
	startup := buildStartupPacket(map[string]string{"user": "hoop"})
	local := newFakeLocal(append(append([]byte{}, ssl...), startup...))

	go func() {
		_ = waitForSent(t, ft, 1, 2*time.Second)
		ft.push(&pb.Packet{
			Type: pbclient.SessionOpenOK,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID: []byte(sessionID),
				pb.SpecConnectionType:   []byte(pb.ConnectionTypePostgres.String()),
			},
		})

		// Wait for the startup packet to be forwarded as PGConnectionWrite.
		deadline := time.After(2 * time.Second)
		for {
			var sawStartup bool
			for _, p := range ft.sentPackets() {
				if pb.PacketType(p.Type) == pbagent.PGConnectionWrite &&
					len(p.Payload) >= 8 && binary.BigEndian.Uint32(p.Payload[4:8]) == 196608 {
					sawStartup = true
				}
			}
			if sawStartup {
				break
			}
			select {
			case <-deadline:
				t.Errorf("timeout waiting for forwarded startup packet")
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
		ft.push(&pb.Packet{
			Type: pbclient.SessionClose,
			Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)},
		})
	}()

	err := runPipe(context.Background(), ft, local, PipeOptions{
		ConnectionName:     "pg-prod",
		SessionOpenTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("runPipe returned error: %v", err)
	}

	// The pump must have written the single 'N' byte back to local before
	// forwarding the startup packet.
	written := local.writtenBytes()
	if len(written) == 0 || written[0] != byte(pgtypes.ServerSSLNotSupported) {
		t.Errorf("expected first local write to be 'N' (SSL not supported), got %v", written)
	}
}
