//go:build integration

package testutil

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

// Client-facing credentials. libhoop's MongoDB proxy is a man-in-the-middle
// for authentication: it authenticates to the upstream server with the real
// credentials from the connection string, but presents a *fake* SCRAM
// server to the client using these hardcoded credentials. The driver must
// therefore connect with noop/noop against authSource=admin, regardless of
// the real upstream user. See libhoop/agent/mongodb/scram.go.
const (
	mongoProxyUser = "noop"
	mongoProxyPass = "noop"
)

// PipedMongoClient drives a real go.mongodb.org/mongo-driver client whose
// underlying transport is bridged to a MockTransport. From the test's
// perspective it's a regular *mongo.Client.
//
// Why a real driver instead of hand-rolled wire bytes
//
// libhoop's MongoDB proxy performs a SCRAM man-in-the-middle: it decodes
// the client's speculative-auth hello (legacy OP_QUERY), runs a full SCRAM
// conversation with the upstream using the real credentials, and
// simultaneously runs a fake SCRAM *server* against the client using
// noop/noop. Reproducing the client half of SCRAM-SHA-256 (client/server
// nonces, salted password proofs, channel binding) by hand would be
// hundreds of lines of fragile crypto. Driving the actual mongo-driver
// client gives us a correct, spec-compliant client for free; the bridge
// only shuttles framed wire messages.
//
// Multiple connections
//
// Unlike the single-connection SQL bridges, the mongo driver opens several
// connections (a topology monitor plus the operation pool). Each
// connection gets its own (connID, net.Pipe, agent-side proxy), allocated
// lazily in the dialer. This mirrors the production client proxy
// (client/proxy/mongodb.go), which assigns a fresh connectionID per
// accepted TCP connection.
//
// Wire framing
//
// MongoDB messages are length-prefixed (a 16-byte header whose first 4
// bytes are the total message length). The agent-side proxy decodes
// exactly one message per Write, so the outbound pump must frame the
// driver's byte stream into whole messages — it cannot forward arbitrary
// chunks. This mirrors copyMongoDBBuffer in the production proxy.
type PipedMongoClient struct {
	// Client is the configured *mongo.Client backed by the bridge.
	Client *mongo.Client

	sessionID string
	tr        *MockTransport
	demux     *RecvDemux

	connSeq atomic.Int64

	mu     sync.Mutex
	conns  []*mongoBridgeConn
	cancel context.CancelFunc
	ctx    context.Context

	done      chan struct{}
	closeOnce sync.Once
}

// mongoBridgeConn is a single bridged connection: one net.Pipe whose driver
// end is handed to the mongo driver and whose bridge end is pumped to/from
// the agent under a unique connID.
type mongoBridgeConn struct {
	connID     string
	driverConn net.Conn
	bridgeConn net.Conn
}

// DialPipedMongo builds the bridge for an already-open MongoDB session and
// returns a PipedMongoClient with a ready *mongo.Client. The session must
// already be open at the agent (call OpenMongoSession) and the demux must
// already be running (call StartRecvDemux) before dialing.
//
// connIDPrefix namespaces the per-connection IDs this client allocates so
// concurrent clients on the same agent don't collide.
//
// Cleanup is automatic via t.Cleanup; tests don't need to defer Close.
func DialPipedMongo(t *testing.T, tr *MockTransport, demux *RecvDemux, mc *MongoContainer, sessionID, connIDPrefix string) *PipedMongoClient {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	p := &PipedMongoClient{
		sessionID: sessionID,
		tr:        tr,
		demux:     demux,
		cancel:    cancel,
		ctx:       ctx,
		done:      make(chan struct{}),
	}

	dialer := &mongoPipeDialer{p: p, prefix: connIDPrefix}

	// The driver authenticates against the proxy's fake SCRAM server with
	// noop/noop. directConnection=true keeps topology discovery minimal
	// (single host, no SRV/replica-set probing). No ServerAPIOptions and
	// no loadBalanced so the driver issues the legacy OP_QUERY hello that
	// libhoop's proxy requires to trigger its auth-bypass path.
	uri := fmt.Sprintf("mongodb://%s:%s@bridge:27017/?authSource=admin&directConnection=true",
		mongoProxyUser, mongoProxyPass)

	opts := options.Client().
		ApplyURI(uri).
		SetDialer(dialer).
		SetServerSelectionTimeout(20 * time.Second).
		SetConnectTimeout(20 * time.Second).
		SetTimeout(30 * time.Second)

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		cancel()
		t.Fatalf("mongo bridge: failed to connect: %v", err)
	}
	p.Client = client

	t.Cleanup(func() { p.Close() })
	return p
}

// mongoPipeDialer allocates a fresh bridged connection per DialContext.
type mongoPipeDialer struct {
	p      *PipedMongoClient
	prefix string
}

func (d *mongoPipeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.p.newConn(d.prefix), nil
}

// newConn allocates a new (connID, net.Pipe), registers it, and starts its
// pumps. The driver end of the pipe is returned to the caller.
func (p *PipedMongoClient) newConn(prefix string) net.Conn {
	select {
	case <-p.done:
		// Bridge torn down; hand back a closed pipe so the driver's dial
		// fails cleanly rather than hanging.
		dc, bc := net.Pipe()
		_ = bc.Close()
		return dc
	default:
	}

	seq := p.connSeq.Add(1)
	connID := fmt.Sprintf("%s-%d", prefix, seq)
	driverConn, bridgeConn := net.Pipe()
	c := &mongoBridgeConn{
		connID:     connID,
		driverConn: driverConn,
		bridgeConn: bridgeConn,
	}

	p.mu.Lock()
	p.conns = append(p.conns, c)
	p.mu.Unlock()

	go p.outboundPump(c)
	go p.inboundPump(c)

	return driverConn
}

// outboundPump frames the driver's byte stream into whole MongoDB messages
// and forwards each as its own MongoDBConnectionWrite packet for this
// connection.
func (p *PipedMongoClient) outboundPump(c *mongoBridgeConn) {
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}
		msg, err := readMongoMessage(c.bridgeConn)
		if len(msg) > 0 {
			p.sendWrite(c.connID, msg)
		}
		if err != nil {
			return
		}
	}
}

// maxMongoMessageSize bounds a single inbound wire message, mirroring the
// production client proxy's maxPacketSize (client/proxy/mongodb.go) so the
// bridge surfaces the same failure shape as production rather than
// allocating an arbitrarily large buffer from a malformed length prefix.
const maxMongoMessageSize = 16 * 1024 * 1024 // 16 MiB

// readMongoMessage reads exactly one MongoDB wire message: a 16-byte header
// whose first 4 bytes are the little-endian total message length (header
// included), followed by the body.
func readMongoMessage(r io.Reader) ([]byte, error) {
	var header [16]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	total := int(binary.LittleEndian.Uint32(header[0:4]))
	if total < len(header) {
		return nil, fmt.Errorf("mongo bridge: invalid message length %d", total)
	}
	if total > maxMongoMessageSize {
		return nil, fmt.Errorf("mongo bridge: message too large (max:%d, got:%d)", maxMongoMessageSize, total)
	}
	msg := make([]byte, total)
	copy(msg, header[:])
	if _, err := io.ReadFull(r, msg[len(header):]); err != nil {
		return nil, err
	}
	return msg, nil
}

// inboundPump copies the payloads of MongoDBConnectionWrite packets the
// agent emits for this connection back into the driver's net.Conn. The
// proxy already emits whole messages, so raw streaming is fine here — the
// driver reassembles by length prefix.
func (p *PipedMongoClient) inboundPump(c *mongoBridgeConn) {
	if p.demux == nil {
		return
	}
	ch := p.demux.Channel(c.connID)
	for {
		select {
		case <-p.ctx.Done():
			return
		case pkt, ok := <-ch:
			if !ok {
				return
			}
			if len(pkt.Payload) == 0 {
				continue
			}
			if _, err := c.bridgeConn.Write(pkt.Payload); err != nil {
				return
			}
		}
	}
}

func (p *PipedMongoClient) sendWrite(connID string, payload []byte) {
	select {
	case <-p.done:
		return
	default:
	}
	defer func() { _ = recover() }() // tolerate inject racing a transport close
	pkt := &pb.Packet{
		Type: pbagent.MongoDBConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(p.sessionID),
			pb.SpecClientConnectionID: []byte(connID),
		},
		Payload: payload,
	}
	p.tr.Inject(pkt)
}

// SessionID returns the agent-side session ID.
func (p *PipedMongoClient) SessionID() string { return p.sessionID }

// ConnCount returns how many distinct bridged connections (and therefore
// distinct agent-side proxies / connIDs) the driver has opened so far. The
// mongo driver opens a topology monitor plus an operation pool, so a
// healthy client allocates more than one. Tests use this to verify the
// per-DialContext connID allocation actually routes multiple connections.
func (p *PipedMongoClient) ConnCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

// Close disconnects the *mongo.Client and tears down every bridged
// connection and its pumps. Safe to call multiple times.
func (p *PipedMongoClient) Close() {
	p.closeOnce.Do(func() {
		close(p.done)
		if p.Client != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = p.Client.Disconnect(ctx)
			cancel()
		}
		p.cancel()
		p.mu.Lock()
		conns := p.conns
		p.conns = nil
		p.mu.Unlock()
		for _, c := range conns {
			_ = c.driverConn.Close()
			_ = c.bridgeConn.Close()
		}
	})
}

// PingWithTimeout runs Client.Ping bounded by timeout. Establishing the
// first connection forces the full bridged SCRAM handshake through libhoop,
// so a successful ping is the integration smoke test for MongoDB auth.
func (p *PipedMongoClient) PingWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return p.Client.Ping(ctx, nil)
}
