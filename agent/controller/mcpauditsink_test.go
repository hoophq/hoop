package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/config"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/common/proto/spectypes"
	"github.com/hoophq/mcpproxy/audit"
)

// blockingTransport is the stream contention the sink exists to absorb: a
// client.Send that does not return until the test lets it. The real
// mutexClient serializes every packet the agent produces behind one lock and
// one gRPC stream, so a single slow send stalls all of them.
type blockingTransport struct {
	release chan struct{}

	mu   sync.Mutex
	sent []*pb.Packet
	// entered reports that a Send is parked inside the transport.
	entered chan struct{}
}

func newBlockingTransport() *blockingTransport {
	return &blockingTransport{
		release: make(chan struct{}),
		entered: make(chan struct{}, 1),
	}
}

func (t *blockingTransport) Send(pkt *pb.Packet) error {
	select {
	case t.entered <- struct{}{}:
	default:
	}
	<-t.release
	t.mu.Lock()
	t.sent = append(t.sent, pkt)
	t.mu.Unlock()
	return nil
}

func (t *blockingTransport) packets() []*pb.Packet {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*pb.Packet(nil), t.sent...)
}

func (t *blockingTransport) Recv() (*pb.Packet, error)      { select {} }
func (t *blockingTransport) StreamContext() context.Context { return context.Background() }
func (t *blockingTransport) StartKeepAlive()                {}
func (t *blockingTransport) Close() (error, error)          { return nil, nil }

func testEvent(tool string) audit.Event {
	return audit.Event{Time: time.Now(), Type: "mcp.tool_call", Session: "sid-1", Tool: tool}
}

// Emit runs inside the inspection pipeline, on the path of a tool call the
// user is waiting for. It must return regardless of what the gRPC stream is
// doing — a wedged stream may cost audit records, never latency.
func TestMCPAuditSinkEmitDoesNotBlockOnAStalledStream(t *testing.T) {
	transport := newBlockingTransport()
	defer close(transport.release)
	agent := New(transport, &config.Config{}, nil)

	sink := agent.mcpAuditSink("sid-1", map[string][]byte{pb.SpecGatewaySessionID: []byte("sid-1")})
	defer sink.stop()

	// One event to park the drain goroutine inside Send, so the queue is the
	// only thing absorbing everything after it.
	sink.Emit(context.Background(), testEvent("first"))
	select {
	case <-transport.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("drain goroutine never reached client.Send")
	}

	// Overfill the queue: past mcpAuditQueueSize, Emit must drop rather than
	// wait for room.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range mcpAuditQueueSize * 3 {
			sink.Emit(context.Background(), testEvent("flood"))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Emit blocked on a full queue instead of dropping")
	}
	if n := sink.dropped.Load(); n == 0 {
		t.Fatal("queue absorbed more than its bound without dropping")
	}
}

// The packet the sink puts on the wire is what the gateway records, so its
// type, marker and spec must survive the trip through the queue.
func TestMCPAuditSinkSendsTaggedPacket(t *testing.T) {
	transport := newBlockingTransport()
	close(transport.release) // never block
	agent := New(transport, &config.Config{}, nil)

	spec := map[string][]byte{
		pb.SpecGatewaySessionID:   []byte("sid-1"),
		pb.SpecClientConnectionID: []byte("conn-9"),
	}
	sink := agent.mcpAuditSink("sid-1", spec)
	sink.Emit(context.Background(), testEvent("whoami"))
	sink.stop()
	waitSinkStopped(t, sink)

	pkts := transport.packets()
	if len(pkts) != 1 {
		t.Fatalf("sent %d packets, want 1", len(pkts))
	}
	pkt := pkts[0]
	if pkt.Type != pbclient.MCPProxyConnectionWrite {
		t.Fatalf("packet type = %q, want %q", pkt.Type, pbclient.MCPProxyConnectionWrite)
	}
	if len(pkt.Spec[pb.SpecMCPEventKey]) == 0 {
		t.Fatal("event packet is missing the marker; the gateway would forward it to the MCP client as response bytes")
	}
	if got := string(pkt.Spec[pb.SpecClientConnectionID]); got != "conn-9" {
		t.Fatalf("connection id = %q, want conn-9", got)
	}
	if !strings.HasSuffix(string(pkt.Payload), "\n") {
		t.Fatalf("payload is not a line: %q", pkt.Payload)
	}
	var ev audit.Event
	if err := json.Unmarshal(pkt.Payload, &ev); err != nil {
		t.Fatalf("payload is not a json audit record: %v", err)
	}
	if ev.Tool != "whoami" {
		t.Fatalf("tool = %q, want whoami", ev.Tool)
	}
}

// Events are only meaningful as a sequence (call, approval, result), so the
// queue must not reorder them.
func TestMCPAuditSinkPreservesOrder(t *testing.T) {
	transport := newBlockingTransport()
	close(transport.release)
	agent := New(transport, &config.Config{}, nil)

	sink := agent.mcpAuditSink("sid-1", nil)
	const total = 200
	for i := range total {
		sink.Emit(context.Background(), audit.Event{Type: "mcp.tool_call", Fields: map[string]any{"i": i}})
	}
	sink.stop()
	waitSinkStopped(t, sink)

	pkts := transport.packets()
	if len(pkts) != total {
		t.Fatalf("sent %d packets, want %d (stop must flush what is queued)", len(pkts), total)
	}
	for i, pkt := range pkts {
		var ev audit.Event
		if err := json.Unmarshal(pkt.Payload, &ev); err != nil {
			t.Fatalf("packet %d is not json: %v", i, err)
		}
		if got := ev.Fields["i"].(float64); int(got) != i {
			t.Fatalf("packet %d carries event %v: the queue reordered events", i, got)
		}
	}
}

// Session cleanup must end the drain goroutine. One leaked goroutine per MCP
// session is a leak that grows with agent uptime, and it holds the session's
// spec map alive with it.
func TestSessionCleanupStopsMCPAuditSink(t *testing.T) {
	transport := newBlockingTransport()
	close(transport.release)
	agent := New(transport, &config.Config{}, nil)

	sink := agent.mcpAuditSink("sid-cleanup", nil)
	agent.mcpGateways.Store("sid-cleanup", &mcpGatewayHolder{sink: sink})

	agent.closeMCPProxyConnections("sid-cleanup")
	waitSinkStopped(t, sink)

	// Emitting after cleanup must not panic or block; the packet is simply
	// buffered and never sent.
	sink.Emit(context.Background(), testEvent("late"))
	// And a second stop is a no-op rather than a close-of-closed-channel.
	sink.stop()
}

// End to end: a real tool call through a real gateway must still produce audit
// packets on the wire. The queue sits between the pipeline and client.Send, so
// this is what proves the indirection did not silently swallow the stream.
func TestMCPAuditEventsReachTheStream(t *testing.T) {
	sessionID := "sid-audit-e2e"
	backend, transport := newTunnelPair(t, sessionID, nil)
	agent := backend.agent

	connParams := agent.connectionParams(sessionID)
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)}
	gw, err := agent.mcpGatewayFor(sessionID, connenv, connParams,
		mcpProxyOpts(connenv, connParams, sessionID, "1"), spec)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}

	h := gw.Handler()
	_, sid, _ := mcpPost(t, h, "", mcpInitialize)
	if sid == "" {
		t.Fatal("initialize did not open a session")
	}
	mcpPost(t, h, sid, mcpToolsList)
	mcpPost(t, h, sid, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`)

	// Cleanup stops the sink, which flushes; after it returns every event the
	// session produced has been handed to the transport.
	agent.closeMCPProxyConnections(sessionID)

	var events []*pb.Packet
	for _, pkt := range transport.packetsOfType(pbclient.MCPProxyConnectionWrite) {
		if len(pkt.Spec[pb.SpecMCPEventKey]) > 0 {
			events = append(events, pkt)
		}
	}
	if len(events) == 0 {
		t.Fatal("a full tool call produced no audit events on the stream")
	}
	var sawToolCall bool
	for _, pkt := range events {
		var ev audit.Event
		if err := json.Unmarshal(pkt.Payload, &ev); err != nil {
			t.Fatalf("audit packet is not json: %s", pkt.Payload)
		}
		if ev.Tool == "whoami" {
			sawToolCall = true
		}
	}
	if !sawToolCall {
		t.Fatalf("no audit event names the tool that ran; got %d events", len(events))
	}
}

// A wedged stream must not keep the drain goroutine alive past its session:
// stop gives up on the first failing write instead of retrying the backlog.
func TestMCPAuditSinkStopReturnsOnAStalledStream(t *testing.T) {
	transport := newBlockingTransport()
	agent := New(transport, &config.Config{}, nil)

	sink := agent.mcpAuditSink("sid-1", nil)
	sink.Emit(context.Background(), testEvent("first"))
	select {
	case <-transport.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("drain goroutine never reached client.Send")
	}
	sink.Emit(context.Background(), testEvent("second"))

	sink.stop()
	close(transport.release) // the parked Send completes, then flush runs
	waitSinkStopped(t, sink)
}

// failingTransport fails every Send after the first n succeed, and counts the
// attempts. It is how flush's give-up-on-first-error branch becomes
// observable: a wedged stream is indistinguishable from a slow one unless the
// error is real.
type failingTransport struct {
	mu        sync.Mutex
	okBefore  int
	attempts  int
	delivered int
}

func (t *failingTransport) Send(*pb.Packet) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attempts++
	if t.attempts <= t.okBefore {
		t.delivered++
		return nil
	}
	return errors.New("stream is gone")
}

func (t *failingTransport) counts() (attempts, delivered int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.attempts, t.delivered
}

func (t *failingTransport) Recv() (*pb.Packet, error)      { select {} }
func (t *failingTransport) StreamContext() context.Context { return context.Background() }
func (t *failingTransport) StartKeepAlive()                {}
func (t *failingTransport) Close() (error, error)          { return nil, nil }

// flush abandons the backlog on the first failing write rather than walking
// it. The backlog can be a thousand events, and a stream that failed once at
// session teardown is not coming back: retrying each one holds the drain
// goroutine — and the sessionCleanup that is waiting on it — open for a
// thousand more doomed gRPC calls.
func TestMCPAuditSinkFlushAbandonsTheBacklogOnTheFirstError(t *testing.T) {
	// A few writes land before the stream dies, so the test distinguishes
	// "gave up at the error" from "never wrote anything".
	const okBefore = 3
	transport := &failingTransport{okBefore: okBefore}
	agent := New(transport, &config.Config{}, nil)

	sink := agent.mcpAuditSink("sid-flush", nil)
	// Stop the drain goroutine before emitting anything: with no reader
	// running, the whole batch lands in the queue and flush sees one backlog.
	// The flush stop performs finds it empty and writes nothing.
	sink.stop()
	waitSinkStopped(t, sink)

	const backlog = 50
	for i := range backlog {
		sink.Emit(context.Background(), testEvent(fmt.Sprintf("tool-%d", i)))
	}
	// The drain goroutine has already exited, so flush is invoked directly —
	// this is the branch under test, not the lifecycle around it.
	sink.flush()

	attempts, delivered := transport.counts()
	if delivered != okBefore {
		t.Fatalf("delivered %d events, want %d: flush must drain what it can before the stream fails", delivered, okBefore)
	}
	if attempts != okBefore+1 {
		t.Fatalf("flush attempted %d writes, want %d: it must abandon the backlog at the first error, not walk all %d",
			attempts, okBefore+1, backlog)
	}
	// The events it gave up on stay queued rather than being silently
	// consumed, so nothing pretends they were written.
	if got, want := len(sink.queue), backlog-okBefore-1; got != want {
		t.Errorf("queue holds %d events after the aborted flush, want %d", got, want)
	}
}

// An analyzer verdict must leave through the sink's queue, never through
// client.Send on the caller's goroutine.
//
// shipVerdict runs inside Inspect, on the path of a tool call the user is
// waiting for. Sending inline takes the agent-wide send mutex there — behind
// whatever multi-megabyte response chunk another session is writing — which is
// the exact stall the sink was built to keep off this path.
func TestMCPVerdictLeavesThroughTheSinkNotTheCallersGoroutine(t *testing.T) {
	transport := newBlockingTransport()
	agent := New(transport, &config.Config{}, nil)

	sink := agent.mcpAuditSink("sid-verdict", nil)

	// Park the drain goroutine inside Send, so the stream is as contended as
	// it gets: anything that ships inline blocks here.
	sink.Emit(context.Background(), testEvent("first"))
	select {
	case <-transport.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("drain goroutine never reached client.Send")
	}

	shipped := make(chan struct{})
	go func() {
		defer close(shipped)
		mcpVerdictEmitter(sink)([]byte("encoded-verdict"))
	}()
	select {
	case <-shipped:
	case <-time.After(5 * time.Second):
		t.Fatal("shipping a verdict blocked on the stream; it took client.Send on the tool-call goroutine instead of the sink queue")
	}

	// Queued, not sent: the parked Send still owns the stream.
	if len(transport.packets()) != 0 {
		t.Fatal("a packet reached the transport while the stream was parked; the verdict bypassed the queue")
	}

	close(transport.release)
	sink.stop()
	waitSinkStopped(t, sink)

	var verdicts int
	for _, pkt := range transport.packets() {
		if raw := pkt.Spec[spectypes.AIAnalyzerInfoKey]; len(raw) > 0 {
			verdicts++
			if string(raw) != "encoded-verdict" {
				t.Errorf("verdict payload = %q, want the encoded one", raw)
			}
			if len(pkt.Spec[pb.SpecMCPEventKey]) == 0 {
				t.Error("verdict packet lost its event marker in the queue; the gateway would forward it to the MCP client as response bytes")
			}
			if pkt.Type != pbclient.MCPProxyConnectionWrite {
				t.Errorf("verdict packet type = %q, want %q", pkt.Type, pbclient.MCPProxyConnectionWrite)
			}
		}
	}
	if verdicts != 1 {
		t.Fatalf("%d verdicts reached the stream, want 1", verdicts)
	}
}

// The verdict rides its own packet. Stamping the shared spec map instead would
// leak the verdict onto every audit event emitted afterwards, and the gateway
// dedupes on (ConnID, Seq) — so the duplicates would not even be visible as
// duplicates, they would just re-attribute later events to this verdict.
func TestMCPVerdictDoesNotContaminateLaterEvents(t *testing.T) {
	transport := newBlockingTransport()
	close(transport.release)
	agent := New(transport, &config.Config{}, nil)

	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte("sid-verdict-2")}
	sink := agent.mcpAuditSink("sid-verdict-2", spec)

	mcpVerdictEmitter(sink)([]byte("encoded-verdict"))
	sink.Emit(context.Background(), testEvent("after"))
	sink.stop()
	waitSinkStopped(t, sink)

	pkts := transport.packets()
	if len(pkts) != 2 {
		t.Fatalf("sent %d packets, want 2", len(pkts))
	}
	if len(pkts[1].Spec[spectypes.AIAnalyzerInfoKey]) != 0 {
		t.Error("the audit event after the verdict carries it too; the verdict mutated the sink's shared spec")
	}
	if len(spec[spectypes.AIAnalyzerInfoKey]) != 0 {
		t.Error("the verdict was written into the caller's spec map")
	}
}

// waitSinkStopped blocks until the sink's drain goroutine has exited.
func waitSinkStopped(t *testing.T, s *mcpEventSink) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("drain goroutine still running after stop")
	}
}
