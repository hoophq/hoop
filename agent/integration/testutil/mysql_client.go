//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

// PipedMySQLClient drives a real go-sql-driver/mysql connection whose
// underlying transport is bridged to a MockTransport. From the test's
// perspective it's a regular *sql.DB: call Exec/Query and get results.
//
// Why a real driver instead of hand-rolled wire bytes
//
// Unlike libhoop's Postgres proxy (a transparent byte pump), libhoop's
// MySQL proxy is a man-in-the-middle that speaks the full handshake with
// the client: it forwards the upstream server greeting to the client,
// reads the client's handshake response (auth plugin negotiation,
// scramble, capability flags), then completes auth with the upstream
// itself. Reproducing that handshake by hand — caching_sha2_password /
// mysql_native_password scramble, capability flag math, packet
// sequencing — would be hundreds of lines of fragile protocol code.
// Driving the actual go-sql-driver client gives us a correct client for
// free; the bridge only shuttles raw bytes.
//
// Wire flow
//
//	test            PipedMySQLClient            MockTransport / Agent
//	────            ────────────────            ─────────────────────
//	db.Query()      go-sql-driver               MySQLConnectionWrite
//	  → bytes  ───► net.Pipe ───► outbound pump ───► Inject()
//	                                              │
//	  ← bytes  ◄─── net.Pipe ◄─── inbound pump ◄── RecvCh (demux)
//
// The bridge is purely byte-oriented: MySQL is a single full-duplex
// stream per connection (no channel multiplexing like SSH), so each
// direction is a straight copy between the driver's net.Conn and a
// MySQLConnectionWrite packet.
type PipedMySQLClient struct {
	// DB is the configured *sql.DB backed by the bridged connection.
	// Tests call DB.Exec, DB.Query, DB.Ping directly.
	DB *sql.DB

	sessionID string
	connID    string
	tr        *MockTransport
	demux     *RecvDemux

	driverConn net.Conn // the go-sql-driver side of the pipe
	bridgeConn net.Conn // the bridge side of the pipe

	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

// registry tracks live bridges by a unique DSN host token so the
// registered dial func can hand the driver the right pipe. go-sql-driver
// dials lazily (on first use of the *sql.DB), and RegisterDialContext is
// process-global, so we route by an opaque per-client key embedded in the
// DSN rather than by address.
var (
	mysqlBridgeRegistry   sync.Map // map[string]net.Conn
	mysqlDialerRegistered atomic.Bool
)

func registerMySQLDialer() {
	if mysqlDialerRegistered.CompareAndSwap(false, true) {
		mysql.RegisterDialContext("hoopbridge", func(ctx context.Context, addr string) (net.Conn, error) {
			v, ok := mysqlBridgeRegistry.LoadAndDelete(addr)
			if !ok {
				return nil, fmt.Errorf("hoopbridge: no bridge registered for %q", addr)
			}
			return v.(net.Conn), nil
		})
	}
}

// DialPipedMySQL builds the net.Pipe bridge for an already-open MySQL
// session and returns a PipedMySQLClient with a ready *sql.DB. The DB is
// configured to a single connection so test assertions about upstream
// connection counts are deterministic.
//
// The session must already be open at the agent (call OpenMySQLSession)
// and the demux must already be running (call StartRecvDemux) before
// dialing — the inbound pump reads the server greeting off the demux as
// soon as the driver's first write triggers libhoop's upstream dial.
//
// Cleanup is automatic via t.Cleanup; tests don't need to defer Close.
func DialPipedMySQL(t *testing.T, tr *MockTransport, demux *RecvDemux, mc *MySQLContainer, sessionID, connID string) *PipedMySQLClient {
	t.Helper()
	registerMySQLDialer()

	driverConn, bridgeConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	p := &PipedMySQLClient{
		sessionID:  sessionID,
		connID:     connID,
		tr:         tr,
		demux:      demux,
		driverConn: driverConn,
		bridgeConn: bridgeConn,
		cancel:     cancel,
		done:       make(chan struct{}),
	}

	// Register the driver side of the pipe under a unique token, then
	// point the DSN at it. go-sql-driver calls our dial func with this
	// token as the address.
	bridgeAddr := uuid.New().String()
	mysqlBridgeRegistry.Store(bridgeAddr, driverConn)

	// parseTime=false keeps results as raw bytes/strings which is all the
	// tests need. The user/password here must match what libhoop uses to
	// auth upstream (it reads them from connection env vars, not the
	// DSN), but the driver still needs *a* user in its handshake response
	// for libhoop to parse; use the real one.
	dsn := fmt.Sprintf("%s:%s@hoopbridge(%s)/%s?timeout=30s&readTimeout=30s&writeTimeout=30s",
		mc.User, mc.Password, bridgeAddr, mc.Database)

	connector, err := mysql.NewConnector(mustParseDSN(t, dsn))
	if err != nil {
		cancel()
		t.Fatalf("mysql bridge: failed to build connector: %v", err)
	}
	db := sql.OpenDB(connector)
	// One connection: the bridge backs exactly one net.Pipe. A pool >1
	// would have the driver try to dial a second bridge conn that doesn't
	// exist.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	p.DB = db

	// Start the byte pumps before the driver dials so the handshake
	// (server greeting → client response → auth result) can flow.
	go p.outboundPump(ctx)
	go p.inboundPump(ctx)

	// Bootstrap the upstream connection. In MySQL the *server* speaks
	// first (sends the greeting), but libhoop only dials the upstream —
	// and thus only produces the greeting — once it receives the first
	// MySQLConnectionWrite for this (sid, connID). The go-sql-driver
	// client, conversely, blocks reading the greeting before it writes
	// anything. That's a bootstrap deadlock: neither side moves first.
	//
	// Break it with a zero-length trigger packet. processMySQLProtocol
	// treats the first packet as the proxy-creation signal and does not
	// forward its payload upstream (the client handshake response is read
	// from libhoop's clientInitBuffer on a *later* write), so an empty
	// payload is safe — it only kicks off MySQL()/Run(), which emits the
	// greeting the driver is waiting for.
	p.sendWrite(nil)

	t.Cleanup(func() { p.Close() })
	return p
}

// outboundPump copies bytes the driver writes (handshake response,
// COM_QUERY, COM_QUIT, ...) into MySQLConnectionWrite packets bound for
// the agent.
func (p *PipedMySQLClient) outboundPump(ctx context.Context) {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := p.bridgeConn.Read(buf)
		if n > 0 {
			p.sendWrite(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			return
		}
	}
}

// inboundPump copies the payloads of MySQLConnectionWrite packets the
// agent emits for this connection (server greeting, auth result, result
// sets) back into the driver's net.Conn.
func (p *PipedMySQLClient) inboundPump(ctx context.Context) {
	if p.demux == nil {
		return
	}
	ch := p.demux.Channel(p.connID)
	for {
		select {
		case <-ctx.Done():
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

func (p *PipedMySQLClient) sendWrite(payload []byte) {
	select {
	case <-p.done:
		return
	default:
	}
	defer func() { _ = recover() }() // tolerate inject racing a transport close
	pkt := &pb.Packet{
		Type: pbagent.MySQLConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(p.sessionID),
			pb.SpecClientConnectionID: []byte(p.connID),
		},
		Payload: payload,
	}
	p.tr.Inject(pkt)
}

// SessionID returns the agent-side session ID.
func (p *PipedMySQLClient) SessionID() string { return p.sessionID }

// ConnID returns the gateway-side connection ID this client occupies.
func (p *PipedMySQLClient) ConnID() string { return p.connID }

// Close tears down the *sql.DB, both pipe ends, and the pumps. Safe to
// call multiple times.
func (p *PipedMySQLClient) Close() {
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

func mustParseDSN(t *testing.T, dsn string) *mysql.Config {
	t.Helper()
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("mysql bridge: failed to parse DSN: %v", err)
	}
	return cfg
}

// PingWithTimeout runs DB.PingContext bounded by timeout. Establishing the
// first connection forces the full bridged handshake through libhoop, so a
// successful ping is the integration smoke test for MySQL auth.
func (p *PipedMySQLClient) PingWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return p.DB.PingContext(ctx)
}
