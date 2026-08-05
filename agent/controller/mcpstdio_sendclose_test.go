package controller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/config"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	mcpbackend "github.com/hoophq/mcpproxy/backend"
)

// orderedTransport records packets in the order they reach the stream, and can
// hold one packet type inside Send to open a race window on demand.
//
// Recording happens after the hold, so the log is true wire order: the real
// mutexClient serializes every agent write behind one mutex and one gRPC
// stream, and the gateway forwards to the client in that order.
type orderedTransport struct {
	mu   sync.Mutex
	sent []string

	// gateType names the packet type held inside Send; the first one blocks
	// until release is closed. Later packets of that type pass straight
	// through, so a test gates exactly one write.
	gateType string
	entered  chan struct{}
	release  chan struct{}
	gateOnce sync.Once
}

func newOrderedTransport(gateType string) *orderedTransport {
	return &orderedTransport{
		gateType: gateType,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (t *orderedTransport) Send(pkt *pb.Packet) error {
	if pkt.Type == t.gateType {
		t.gateOnce.Do(func() {
			close(t.entered)
			<-t.release
		})
	}
	t.mu.Lock()
	t.sent = append(t.sent, pkt.Type)
	t.mu.Unlock()
	return nil
}

func (t *orderedTransport) order() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.sent...)
}

func (t *orderedTransport) Recv() (*pb.Packet, error)      { select {} }
func (t *orderedTransport) StreamContext() context.Context { return context.Background() }
func (t *orderedTransport) StartKeepAlive()                {}
func (t *orderedTransport) Close() (error, error)          { return nil, nil }

// newBackendOn builds and starts a tunnelled backend over the given transport.
func newBackendOn(t *testing.T, transport pb.ClientTransport, sessionID string) *clientStdioBackend {
	t.Helper()
	agent := New(transport, &config.Config{}, nil)
	agent.connStore.Set(sessionID, &pb.AgentConnectionParams{
		ConnectionName: "laptop-mcp",
		ConnectionType: string(pb.ConnectionTypeMcpProxy),
		CmdList:        []string{"node", "server.js"},
		EnvVars:        mcpProxyEnvVars(map[string]string{"MCP_TRANSPORT": mcpTransportClientStdio}),
	})
	b, err := agent.clientStdioFactory("laptop-mcp", sessionID)(context.Background())
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	backend := b.(*clientStdioBackend)
	if err := backend.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	return backend
}

func indexOf(order []string, pktType string) int {
	for i, v := range order {
		if v == pktType {
			return i
		}
	}
	return -1
}

// A request must never reach the client after the close that reaps the child
// it is bound for.
//
// The CLI spawns lazily, keyed by backend id: PacketCloseClient reaps the
// child and drops the map entry, so a request arriving afterwards finds
// nothing and spawns a REPLACEMENT MCP server on the user's machine — with its
// reaping packet already spent. That process then outlives the backend,
// holding the user's credentials, until the whole hoop session ends.
//
// The window is real: Send used to check `closed` under b.mu, release it, then
// write. A Close landing entirely inside that gap inverts the wire order.
func TestClientStdioRequestNeverFollowsCloseOnTheWire(t *testing.T) {
	transport := newOrderedTransport(pbclient.MCPStdioRequest)
	backend := newBackendOn(t, transport, "sid-order")

	// A Send parked inside the stream write is exactly the gap Close used to
	// slip through.
	sendDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sendDone <- backend.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	}()
	select {
	case <-transport.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the transport")
	}

	// Session cleanup fires while that write is in flight.
	closeDone := make(chan error, 1)
	go func() { closeDone <- backend.Close() }()

	// Close must not have written anything yet: it is ordered behind the
	// in-flight request, not racing it.
	time.Sleep(100 * time.Millisecond)
	if got := transport.order(); len(got) != 0 {
		t.Fatalf("packets reached the wire while a Send was in flight: %v", got)
	}

	close(transport.release)

	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close never returned")
	}
	select {
	case err := <-sendDone:
		// The backend closed under it; the ack will never come.
		if !errors.Is(err, mcpbackend.ErrClosed) {
			t.Fatalf("Send returned %v, want ErrClosed after the backend closed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send never returned")
	}

	order := transport.order()
	req, cls := indexOf(order, pbclient.MCPStdioRequest), indexOf(order, pbclient.MCPStdioClose)
	if req < 0 || cls < 0 {
		t.Fatalf("wire carried %v, want both a request and a close", order)
	}
	if req > cls {
		t.Fatalf("wire order %v: the request landed after the close, so the CLI "+
			"spawns a replacement mcp server that nothing will ever reap", order)
	}
}

// A Send starting after Close must not write at all. The close already reaped
// the child, so any request behind it strands a new one.
func TestClientStdioSendAfterCloseWritesNothing(t *testing.T) {
	transport := newOrderedTransport("")
	backend := newBackendOn(t, transport, "sid-after-close")

	if err := backend.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	before := len(transport.order())

	if err := backend.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`)); !errors.Is(err, mcpbackend.ErrClosed) {
		t.Fatalf("Send after Close = %v, want ErrClosed", err)
	}
	order := transport.order()
	if len(order) != before {
		t.Fatalf("Send after Close put %v on the wire", order[before:])
	}
}

// sendMu covers the write, not the round trip. Two Sends must both reach the
// wire even though neither is acked: holding the lock across the ack wait
// would let one stalled request block every other one on the backend, which
// for MCP means one slow tool call freezing the session.
func TestClientStdioConcurrentSendsDoNotSerializeOnAcks(t *testing.T) {
	transport := newOrderedTransport("")
	backend := newBackendOn(t, transport, "sid-parallel")

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = backend.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		}()
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(transport.order()) == 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := transport.order(); len(got) != 4 {
		t.Fatalf("%d of 4 requests reached the wire: an unacked Send is holding the write lock", len(got))
	}

	// Release them: Close wakes every waiter through done.
	if err := backend.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	wg.Wait()
}

// deliver runs on the agent's shared recv loop, so it must not wait on a
// network write. Close holds sendMu across its MCPStdioClose write; if that
// write is slow, deliver still has to return — it only needs b.mu, which Close
// releases before writing.
func TestClientStdioDeliverIsNotBlockedByASlowClose(t *testing.T) {
	transport := newOrderedTransport(pbclient.MCPStdioClose)
	backend := newBackendOn(t, transport, "sid-slow-close")

	closeDone := make(chan error, 1)
	go func() { closeDone <- backend.Close() }()
	select {
	case <-transport.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Close never reached the transport")
	}

	delivered := make(chan error, 1)
	go func() { delivered <- backend.deliver([]byte(`{"jsonrpc":"2.0","method":"n"}`)) }()
	select {
	case err := <-delivered:
		if !errors.Is(err, mcpbackend.ErrClosed) {
			t.Fatalf("deliver during Close = %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deliver blocked behind Close's network write; the agent's recv loop is wedged")
	}

	close(transport.release)
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close never returned")
	}
}
