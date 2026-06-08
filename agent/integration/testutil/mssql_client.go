//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	"github.com/hoophq/hoop/common/mssqltypes"
)

// PipedMSSQLClient drives a real github.com/microsoft/go-mssqldb connection
// whose underlying transport is bridged to a MockTransport. From the
// test's perspective it's a regular *sql.DB.
//
// Why a real driver instead of hand-rolled wire bytes
//
// libhoop's MSSQL proxy is a man-in-the-middle that speaks the full TDS
// handshake with the client: it reads the client's PRELOGIN and LOGIN7
// packets, rewrites the encryption option and the credentials, negotiates
// TLS with the upstream itself, and forwards the rewritten login. TDS is a
// frame-based protocol (PRELOGIN, LOGIN7, SQL batch, RPC, token streams)
// that would be hundreds of lines of fragile code to reproduce by hand.
// Driving the actual go-mssqldb client gives us a correct client for free;
// the bridge only shuttles raw bytes.
//
// Unlike the MySQL bridge, no global dial registry is needed: go-mssqldb's
// Connector exposes a Dialer field, so each client installs its own dialer
// that hands back this client's pipe.
//
// Wire flow
//
//	test            PipedMSSQLClient            MockTransport / Agent
//	────            ────────────────            ─────────────────────
//	db.Query()      go-mssqldb                  MSSQLConnectionWrite
//	  → bytes  ───► net.Pipe ───► outbound pump ───► Inject()
//	                                              │
//	  ← bytes  ◄─── net.Pipe ◄─── inbound pump ◄── RecvCh (demux)
//
// The bridge is purely byte-oriented: TDS is a single full-duplex stream
// per connection, so each direction is a straight copy between the
// driver's net.Conn and a MSSQLConnectionWrite packet.
type PipedMSSQLClient struct {
	// DB is the configured *sql.DB backed by the bridged connection.
	// Tests call DB.Exec, DB.Query, DB.Ping directly.
	DB *sql.DB

	sessionID string
	connID    string
	tr        *MockTransport
	demux     *RecvDemux

	driverConn net.Conn // the go-mssqldb side of the pipe
	bridgeConn net.Conn // the bridge side of the pipe

	// packetSize tracks the negotiated TDS packet size. It starts at the
	// default and grows once the client's LOGIN7 advertises a larger size,
	// mirroring the production client proxy (mssqlStreamWriter).
	packetSize atomic.Int64

	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

// pipeDialer implements mssql.HostDialer by always returning the same
// preconnected net.Conn. go-mssqldb dials exactly once per connection in
// the pool, and the *sql.DB is pinned to a single connection, so a
// one-shot dialer is sufficient.
//
// It implements HostDialer (not just Dialer) so go-mssqldb skips its own
// DNS resolution of the DSN host: the bridge host is a placeholder that
// doesn't resolve, and DNS "happens in the dialer's network" — which here
// is just the in-memory pipe. Without HostName() the driver would call
// net.LookupIP("bridge") and fail with "no such host".
type pipeDialer struct {
	host string
	conn net.Conn
	used sync.Once
}

func (d *pipeDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var c net.Conn
	d.used.Do(func() { c = d.conn })
	if c == nil {
		return nil, fmt.Errorf("mssql bridge: dialer already consumed (pool tried to open a second connection)")
	}
	return c, nil
}

// HostName satisfies mssql.HostDialer, signalling that this dialer proxies
// to another network and DNS must not be resolved by the driver.
func (d *pipeDialer) HostName() string { return d.host }

// DialPipedMSSQL builds the net.Pipe bridge for an already-open MSSQL
// session and returns a PipedMSSQLClient with a ready *sql.DB. The DB is
// configured to a single connection so test assertions about upstream
// connection counts are deterministic.
//
// The session must already be open at the agent (call OpenMSSQLSession)
// and the demux must already be running (call StartRecvDemux) before
// dialing — the inbound pump reads the proxy's PRELOGIN reply off the
// demux as soon as the driver's first write triggers proxy creation.
//
// Cleanup is automatic via t.Cleanup; tests don't need to defer Close.
func DialPipedMSSQL(t *testing.T, tr *MockTransport, demux *RecvDemux, mc *MSSQLContainer, sessionID, connID string) *PipedMSSQLClient {
	t.Helper()

	driverConn, bridgeConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	p := &PipedMSSQLClient{
		sessionID:  sessionID,
		connID:     connID,
		tr:         tr,
		demux:      demux,
		driverConn: driverConn,
		bridgeConn: bridgeConn,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	p.packetSize.Store(mssqltypes.DefaultPacketSize)

	// encrypt=disable keeps the client↔proxy link plaintext. The proxy
	// rewrites the server's PRELOGIN reply to advertise "encryption not
	// supported" to the client and offloads TLS to the upstream itself,
	// so the driver must not attempt its own TLS. The user/password here
	// are sent in LOGIN7 but the proxy overrides them with the upstream
	// credentials from the connection env vars; they still need to be
	// present and well-formed for the driver to build the packet.
	dsn := fmt.Sprintf("sqlserver://%s:%s@bridge:1433?database=%s&encrypt=disable&dial+timeout=30&connection+timeout=30",
		mc.User, mc.Password, mc.Database)

	connector, err := mssql.NewConnector(dsn)
	if err != nil {
		cancel()
		t.Fatalf("mssql bridge: failed to build connector: %v", err)
	}
	connector.Dialer = &pipeDialer{host: "bridge", conn: driverConn}

	db := sql.OpenDB(connector)
	// One connection: the bridge backs exactly one net.Pipe. A pool >1
	// would have the driver try to dial a second bridge conn that doesn't
	// exist.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	p.DB = db

	// Start the byte pumps before the driver dials so the handshake
	// (PRELOGIN → PRELOGIN reply → LOGIN7 → login ack) can flow.
	go p.outboundPump(ctx)
	go p.inboundPump(ctx)

	t.Cleanup(func() { p.Close() })
	return p
}

// outboundPump reads complete TDS packets the driver writes (PRELOGIN,
// LOGIN7, SQL batches, ...) and forwards each one as its own
// MSSQLConnectionWrite packet.
//
// The agent-side proxy's handleAuth reads exactly one TDS packet per
// decode (mssqltypes.Decode), and its Write path likewise decodes a single
// packet per call, so the bridge must preserve TDS framing rather than
// stream arbitrary byte chunks. This mirrors the production client proxy
// (client/proxy/mssql.go mssqlStreamWriter), which re-frames the raw
// stream through mssqltypes before sending. Framing by the TDS header
// length is robust to net.Pipe splitting a large write across reads.
func (p *PipedMSSQLClient) outboundPump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		pktBytes, err := p.readTDSPacket()
		if len(pktBytes) > 0 {
			p.handleOutboundPacket(pktBytes)
		}
		if err != nil {
			return
		}
	}
}

// readTDSPacket reads exactly one TDS packet (8-byte header + body) off the
// bridge pipe. The TDS header's bytes [2:4] carry the big-endian total
// packet length including the header.
func (p *PipedMSSQLClient) readTDSPacket() ([]byte, error) {
	var header [8]byte
	if _, err := io.ReadFull(p.bridgeConn, header[:]); err != nil {
		return nil, err
	}
	total := int(binary.BigEndian.Uint16(header[2:4]))
	if total < len(header) {
		return nil, fmt.Errorf("mssql bridge: invalid TDS packet length %d", total)
	}
	pkt := make([]byte, total)
	copy(pkt, header[:])
	if _, err := io.ReadFull(p.bridgeConn, pkt[len(header):]); err != nil {
		return nil, err
	}
	return pkt, nil
}

// handleOutboundPacket re-frames a single TDS packet through mssqltypes
// (canonicalising it the same way the production proxy does) and tracks the
// negotiated packet size from the client's LOGIN7.
func (p *PipedMSSQLClient) handleOutboundPacket(pktBytes []byte) {
	pktList, err := mssqltypes.DecodeFull(pktBytes, int(p.packetSize.Load()))
	if err != nil {
		// Forward the raw bytes if framing fails; the agent will surface
		// the protocol error rather than the bridge silently dropping it.
		p.sendWrite(pktBytes)
		return
	}
	for _, pkt := range pktList {
		if pkt.Type() == mssqltypes.PacketLogin7Type {
			l := mssqltypes.DecodeLogin(pkt.Frame)
			if sz := int64(l.PacketSize()); sz >= minMSSQLPacketSize {
				p.packetSize.Store(sz)
			}
		}
		p.sendWrite(pkt.Encode())
	}
}

// minMSSQLPacketSize matches the production proxy's floor for honouring a
// client-advertised packet size.
const minMSSQLPacketSize = 512

// inboundPump copies the payloads of MSSQLConnectionWrite packets the agent
// emits for this connection (PRELOGIN reply, login ack, token streams)
// back into the driver's net.Conn.
//
// It also subscribes to session-close notifications from the demux. When the
// agent tears down this session, the demux signals the close channel and the
// pump closes bridgeConn, giving the driver an EOF so any blocked query
// returns rather than hanging until the test timeout.
func (p *PipedMSSQLClient) inboundPump(ctx context.Context) {
	if p.demux == nil {
		return
	}
	ch := p.demux.Channel(p.connID)
	closeCh := p.demux.SessionCloseChan(p.sessionID)
	for {
		select {
		case <-ctx.Done():
			return
		case <-closeCh:
			// Agent tore down the session. Close the bridge so the driver
			// receives an EOF and any blocked query returns.
			_ = p.bridgeConn.Close()
			return
		case pkt, ok := <-ch:
			if !ok {
				return
			}
			if len(pkt.Payload) == 0 {
				continue
			}
			if _, err := p.bridgeConn.Write(pkt.Payload); err != nil {
				return
			}
		}
	}
}

func (p *PipedMSSQLClient) sendWrite(payload []byte) {
	select {
	case <-p.done:
		return
	default:
	}
	defer func() { _ = recover() }() // tolerate inject racing a transport close
	pkt := &pb.Packet{
		Type: pbagent.MSSQLConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(p.sessionID),
			pb.SpecClientConnectionID: []byte(p.connID),
		},
		Payload: payload,
	}
	p.tr.Inject(pkt)
}

// SessionID returns the agent-side session ID.
func (p *PipedMSSQLClient) SessionID() string { return p.sessionID }

// ConnID returns the gateway-side connection ID this client occupies.
func (p *PipedMSSQLClient) ConnID() string { return p.connID }

// Close tears down the *sql.DB, both pipe ends, and the pumps. Safe to
// call multiple times.
func (p *PipedMSSQLClient) Close() {
	p.closeOnce.Do(func() {
		close(p.done)
		p.cancel()
		if p.DB != nil {
			_ = p.DB.Close()
		}
		_ = p.driverConn.Close()
		_ = p.bridgeConn.Close()
	})
}

// PingWithTimeout runs DB.PingContext bounded by timeout. Establishing the
// first connection forces the full bridged TDS handshake through libhoop,
// so a successful ping is the integration smoke test for MSSQL auth.
func (p *PipedMSSQLClient) PingWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return p.DB.PingContext(ctx)
}
