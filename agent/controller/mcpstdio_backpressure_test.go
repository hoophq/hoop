package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/config"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	mcpbackend "github.com/hoophq/mcpproxy/backend"
)

// newUndrainedBackend returns a started backend whose Recv nobody reads, which
// is the state the gateway's pump goroutine leaves it in whenever it is busy
// or blocked. No child process and no CLI: the failure under test is entirely
// between the agent's recv loop and the backend.
func newUndrainedBackend(t *testing.T, sessionID string) (*Agent, *clientStdioBackend) {
	t.Helper()
	transport := &loopTransport{}
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
	t.Cleanup(func() { _ = backend.Close() })
	return agent, backend
}

// serverMessage is a packet as it arrives from the user's machine: no request
// id, so processMCPStdioReply routes it to Recv rather than to a Send waiter.
func serverMessage(backend *clientStdioBackend, n int) *pb.Packet {
	return &pb.Packet{
		Type: pbagent.MCPStdioReply,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:    []byte(backend.sessionID),
			pb.SpecMCPStdioBackendKey:  []byte(backend.backendID),
			pb.SpecMCPStdioRequestKey:  nil,
			pb.SpecMCPStdioCommandKey:  nil,
			"mcp.test.sequence-number": []byte(strconv.Itoa(n)),
		},
		Payload: []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"n%d"}`, n)),
	}
}

// deliver runs on the agent's recv loop, which carries every session on this
// agent. Overrunning one backend's Recv buffer must not park it.
func TestClientStdioDeliverNeverBlocksTheRecvLoop(t *testing.T) {
	_, backend := newUndrainedBackend(t, "sid-backpressure")

	// Fill the buffer exactly. Nothing is dropped yet, so a burst up to the
	// bound behaves just like the library's local stdio backend.
	for i := range mcpStdioRecvBuffer {
		if err := backend.deliver([]byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatalf("deliver %d of %d failed before the buffer was full: %v",
				i+1, mcpStdioRecvBuffer, err)
		}
	}

	// One past the bound must return rather than wait for a reader.
	done := make(chan error, 1)
	go func() { done <- backend.deliver([]byte(`{"overflow":true}`)) }()
	select {
	case err := <-done:
		if !errors.Is(err, errMCPStdioRecvFull) {
			t.Fatalf("deliver past the bound returned %v, want errMCPStdioRecvFull", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deliver blocked on a full Recv buffer; the agent's recv loop is wedged")
	}

	// The buffered messages are intact and in order: dropping happens at the
	// tail, it does not disturb what the pump has yet to read.
	for i := range mcpStdioRecvBuffer {
		select {
		case msg := <-backend.Recv():
			if want := fmt.Sprintf(`{"n":%d}`, i); string(msg) != want {
				t.Fatalf("Recv[%d] = %s, want %s", i, msg, want)
			}
		default:
			t.Fatalf("Recv held %d messages, want %d", i, mcpStdioRecvBuffer)
		}
	}
}

// The deadlock this fix exists for.
//
// mcpproxy's pump answers a denied server-initiated request by calling Send
// (gateway/conn.go, inspectS2C), and Send takes b.mu. If deliver blocks on the
// Recv channel while holding b.mu, the recv loop waits for the pump to drain,
// the pump waits for b.mu, and b.mu is held by the recv loop. b.done cannot
// break it: Close needs the same mutex to close it.
//
// The pump is simulated rather than run for real because a real one would have
// to be wedged at exactly this instant; the lock-ordering cycle is identical.
func TestClientStdioDeliverDoesNotDeadlockAgainstSend(t *testing.T) {
	_, backend := newUndrainedBackend(t, "sid-deadlock")

	for i := range mcpStdioRecvBuffer {
		if err := backend.deliver([]byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatalf("deliver %d failed: %v", i+1, err)
		}
	}

	// The recv loop delivers one more message than fits.
	recvLoop := make(chan struct{})
	go func() {
		defer close(recvLoop)
		_ = backend.deliver([]byte(`{"server":"initiated"}`))
	}()

	// The pump reacts by denying a server-initiated request, which calls Send
	// and therefore needs b.mu. Send's own gRPC write goes to loopTransport,
	// which never produces an ack, so it parks on ctx — but it must at least
	// acquire the mutex, which is the half that used to be impossible.
	pump := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		pump <- backend.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000}}`))
	}()

	select {
	case <-recvLoop:
	case <-time.After(5 * time.Second):
		t.Fatal("the recv loop is deadlocked against the gateway pump's Send")
	}
	select {
	case err := <-pump:
		// Whatever Send returns is fine; reaching a return at all proves it
		// got the mutex. Only a hang is a failure.
		_ = err
	case <-time.After(5 * time.Second):
		t.Fatal("Send never acquired the mutex; deliver is still holding it")
	}
}

// Close must not be blocked by a backend nobody is draining. Session cleanup
// runs Close, and a Close that cannot take the mutex leaks the user's child
// process along with the goroutines waiting on it.
func TestClientStdioCloseIsNotBlockedByAFullRecvBuffer(t *testing.T) {
	_, backend := newUndrainedBackend(t, "sid-close-full")

	for i := range mcpStdioRecvBuffer * 2 {
		_ = backend.deliver([]byte(fmt.Sprintf(`{"n":%d}`, i)))
	}

	closed := make(chan error, 1)
	go func() { closed <- backend.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked behind a full Recv buffer; session cleanup would hang")
	}

	select {
	case <-backend.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done was not closed")
	}
	// After Close the drop reason is teardown, not backpressure, so callers
	// log it as routine rather than warning about a falling-behind consumer.
	if err := backend.deliver([]byte(`{"late":true}`)); !errors.Is(err, mcpbackend.ErrClosed) {
		t.Fatalf("deliver after Close = %v, want ErrClosed", err)
	}
}

// The whole path, driven the way the transport does: packets in, no reader on
// Recv. processMCPStdioReply is called inline on the recv loop, so a burst
// well past the bound must still return promptly.
func TestProcessMCPStdioReplyDoesNotBlockOnAFullBackend(t *testing.T) {
	agent, backend := newUndrainedBackend(t, "sid-reply-flood")

	const total = mcpStdioRecvBuffer * 4
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range total {
			agent.processMCPStdioReply(serverMessage(backend, i))
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("processMCPStdioReply blocked after the %d-message buffer filled", mcpStdioRecvBuffer)
	}

	backend.mu.Lock()
	dropped := backend.dropped
	backend.mu.Unlock()
	if want := uint64(total - mcpStdioRecvBuffer); dropped != want {
		t.Fatalf("dropped %d messages, want %d (buffer keeps the first %d)",
			dropped, want, mcpStdioRecvBuffer)
	}
}

// Concurrent delivery and teardown must not panic. deliver holds b.mu across
// the channel send precisely so it cannot race Close closing recv; a send on a
// closed channel would panic the gateway rather than stall it.
func TestClientStdioDeliverRacesCloseWithoutPanicking(t *testing.T) {
	_, backend := newUndrainedBackend(t, "sid-deliver-race")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				_ = backend.deliver([]byte(fmt.Sprintf(`{"n":%d}`, i)))
			}
		}()
	}
	// Drain concurrently so some sends find room and genuinely touch the
	// channel rather than all taking the drop path.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range backend.Recv() {
		}
	}()

	time.Sleep(10 * time.Millisecond)
	if err := backend.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	wg.Wait()
}
