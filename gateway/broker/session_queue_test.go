package broker

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// gatedClientConn releases consumed messages only when the test allows it.
type gatedClientConn struct {
	allow    chan struct{}
	received atomic.Int64
}

func (c *gatedClientConn) Send(data []byte) error {
	<-c.allow
	c.received.Add(int64(len(data)))
	return nil
}
func (c *gatedClientConn) Read() (int, []byte, error) { return 0, nil, nil }
func (c *gatedClientConn) Close() error               { return nil }
func (c *gatedClientConn) WrapToConnection() net.Conn { return nil }

func newQueueTestSession(t *testing.T, client ConnectionCommunicator, budget int64) *Session {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID:                  uuid.New(),
		ClientCommunicator:  client,
		Protocol:            ProtocolRDP,
		dataChannel:         make(chan []byte, 1024),
		credentialsReceived: make(chan bool, 1),
		ctx:                 ctx,
		cancel:              cancel,
		maxQueueBytes:       budget,
	}
	BrokerInstance.sessions.Store(s.ID, s)
	t.Cleanup(s.Close)
	return s
}

func (s *Session) queuedBytesNow() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queuedBytes
}

// TestForwardToTCPByteBudgetRejectsWithoutBlocking verifies that a stalled
// session reaches only its own byte budget and admission returns immediately.
func TestForwardToTCPByteBudgetRejectsWithoutBlocking(t *testing.T) {
	const budget = 64 * 1024
	const msgSize = 16 * 1024

	s := newQueueTestSession(t, &gatedClientConn{allow: make(chan struct{})}, budget)
	for range budget / msgSize {
		if err := s.ForwardToTCP(make([]byte, msgSize)); err != nil {
			t.Fatalf("admit within budget: %v", err)
		}
	}

	start := time.Now()
	if err := s.ForwardToTCP(make([]byte, msgSize)); err != ErrSessionRelayQueueFull {
		t.Fatalf("expected ErrSessionRelayQueueFull, got %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("full per-session queue blocked the shared producer")
	}
	if got := s.queuedBytesNow(); got != budget {
		t.Fatalf("queued bytes=%d, want strict budget %d", got, budget)
	}
}

func TestForwardToTCPRejectsAfterClose(t *testing.T) {
	s := newQueueTestSession(t, &gatedClientConn{allow: make(chan struct{})}, 1024)
	if err := s.ForwardToTCP(make([]byte, 1024)); err != nil {
		t.Fatalf("fill queue: %v", err)
	}
	if err := s.ForwardToTCP(make([]byte, 1)); err != ErrSessionRelayQueueFull {
		t.Fatalf("expected full queue error, got %v", err)
	}

	s.Close()
	if err := s.ForwardToTCP([]byte("late")); err != ErrSessionClosed {
		t.Fatalf("expected ErrSessionClosed, got %v", err)
	}
}

func TestForwardToTCPOversizedMessageIsRejected(t *testing.T) {
	s := newQueueTestSession(t, &gatedClientConn{allow: make(chan struct{})}, 1024)
	if err := s.ForwardToTCP(make([]byte, 64*1024)); err != ErrSessionRelayQueueFull {
		t.Fatalf("expected strict budget rejection, got %v", err)
	}
	if got := s.queuedBytesNow(); got != 0 {
		t.Fatalf("oversized rejection consumed budget: %d", got)
	}
}

// TestSessionConnWrapperNoDataLoss pushes messages far larger than the reader
// chunk size and verifies every byte arrives, in order. The previous wrapper
// spilled at most 16 KiB of remainder into a fixed array and silently dropped
// the rest.
func TestSessionConnWrapperNoDataLoss(t *testing.T) {
	s := newQueueTestSession(t, &gatedClientConn{allow: make(chan struct{})}, maxQueuedBytes)
	conn := s.ToConn()

	// 100 KiB message: with 8 KiB reads the old code lost 100-8-16 = 76 KiB.
	payload := make([]byte, 100*1024)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	if err := s.ForwardToTCP(bytes.Clone(payload)); err != nil {
		t.Fatalf("queue payload: %v", err)
	}

	got := make([]byte, 0, len(payload))
	buf := make([]byte, 8*1024)
	for len(got) < len(payload) {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read failed after %d/%d bytes: %v", len(got), len(payload), err)
		}
		got = append(got, buf[:n]...)
	}

	if !bytes.Equal(got, payload) {
		t.Fatal("relayed bytes differ from sent payload")
	}
}

func TestSessionConnWrapperKeepsUnreadTailInsideBudget(t *testing.T) {
	const budget = 16
	s := newQueueTestSession(t, nil, budget)
	conn := s.ToConn()
	if err := s.ForwardToTCP([]byte("0123456789abcdef")); err != nil {
		t.Fatalf("fill queue: %v", err)
	}

	buf := make([]byte, 4)
	if n, err := conn.Read(buf); err != nil || n != len(buf) {
		t.Fatalf("partial read n=%d err=%v", n, err)
	}
	if got := s.queuedBytesNow(); got != 12 {
		t.Fatalf("charged unread tail=%d, want 12", got)
	}
	if err := s.ForwardToTCP([]byte("12345")); err != ErrSessionRelayQueueFull {
		t.Fatalf("unread tail escaped byte budget: %v", err)
	}

	rest := make([]byte, 12)
	if n, err := io.ReadFull(conn, rest); err != nil || n != len(rest) {
		t.Fatalf("drain tail n=%d err=%v", n, err)
	}
	if got := s.queuedBytesNow(); got != 0 {
		t.Fatalf("queue charge after drain=%d, want 0", got)
	}
}

// TestSessionConnWrapperEOFAfterClose verifies reads return io.EOF once the
// session closes and the queue is drained.
func TestSessionConnWrapperEOFAfterClose(t *testing.T) {
	s := newQueueTestSession(t, &gatedClientConn{allow: make(chan struct{})}, maxQueuedBytes)
	conn := s.ToConn()

	if err := s.ForwardToTCP([]byte("tail")); err != nil {
		t.Fatalf("queue tail: %v", err)
	}
	s.Close()

	// The queued message is still served after close...
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "tail" {
		t.Fatalf("expected queued tail before EOF, got n=%d err=%v", n, err)
	}

	// ...then EOF.
	if _, err := conn.Read(buf); err != io.EOF {
		t.Fatalf("expected io.EOF after drain, got %v", err)
	}
}

// TestSessionConnWrapperDeadline verifies the read deadline fires and remains
// in force until a caller replaces it, matching net.Conn semantics.
func TestSessionConnWrapperDeadline(t *testing.T) {
	s := newQueueTestSession(t, &gatedClientConn{allow: make(chan struct{})}, maxQueuedBytes)
	conn := s.ToConn()

	_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	start := time.Now()
	_, err := conn.Read(make([]byte, 16))
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline fired too late")
	}

	start = time.Now()
	if _, err := conn.Read(make([]byte, 16)); err != context.DeadlineExceeded {
		t.Fatalf("deadline did not persist across reads: %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("expired deadline was reset after one read")
	}
}

func TestSessionConnWrapperDeadlineUpdateWakesBlockedRead(t *testing.T) {
	s := newQueueTestSession(t, &gatedClientConn{allow: make(chan struct{})}, maxQueuedBytes)
	conn := s.ToConn()
	errCh := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 16))
		errCh <- err
	}()

	if err := conn.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	select {
	case err := <-errCh:
		if err != context.DeadlineExceeded {
			t.Fatalf("expected DeadlineExceeded, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline update did not wake blocked Read")
	}
}

// TestForwardToTCPConcurrentClose hammers Close against in-flight producers
// and verifies admission stays panic-free and nonblocking.
func TestForwardToTCPConcurrentClose(t *testing.T) {
	for range 50 {
		client := &gatedClientConn{allow: make(chan struct{})}
		close(client.allow)
		s := newQueueTestSession(t, client, maxQueuedBytes)
		go s.ForwardToClient()

		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 100 {
					_ = s.ForwardToTCP(make([]byte, 128))
				}
			}()
		}
		time.Sleep(time.Millisecond)
		s.Close()
		wg.Wait()
	}
}

type sharedAgentConn struct {
	closes atomic.Int64
	sends  atomic.Int64
}

func (c *sharedAgentConn) Send([]byte) error {
	c.sends.Add(1)
	return nil
}
func (c *sharedAgentConn) Read() (int, []byte, error) { return 0, nil, nil }
func (c *sharedAgentConn) Close() error {
	c.closes.Add(1)
	return nil
}
func (c *sharedAgentConn) WrapToConnection() net.Conn { return nil }

func TestSessionCloseDoesNotCloseSharedAgent(t *testing.T) {
	agent := &sharedAgentConn{}
	first := newQueueTestSession(t, nil, 1024)
	second := newQueueTestSession(t, nil, 1024)
	first.AgentCommunicator = agent
	second.AgentCommunicator = agent

	first.Close()
	if got := agent.closes.Load(); got != 0 {
		t.Fatalf("closing one session closed shared agent %d time(s)", got)
	}
	if err := second.SendToAgent([]byte("still-live")); err != nil {
		t.Fatalf("second session lost shared agent: %v", err)
	}
	if got := agent.sends.Load(); got != 1 {
		t.Fatalf("second session sends=%d, want 1", got)
	}
}

func TestRelayBudgetIsSessionIsolated(t *testing.T) {
	first := newQueueTestSession(t, nil, 4)
	second := newQueueTestSession(t, nil, 4)
	if err := first.ForwardToTCP([]byte("full")); err != nil {
		t.Fatalf("fill first session: %v", err)
	}
	if err := first.ForwardToTCP([]byte("x")); err != ErrSessionRelayQueueFull {
		t.Fatalf("expected first session full, got %v", err)
	}
	if err := second.ForwardToTCP([]byte("live")); err != nil {
		t.Fatalf("first session blocked second: %v", err)
	}
}

func TestAgentInstanceRoutingRejectsForeignConnection(t *testing.T) {
	owner := uuid.New()
	foreign := uuid.New()
	session := newQueueTestSession(t, nil, 1024)
	session.AgentInstanceID = owner

	if got := GetSessionForAgentInstance(session.ID, owner); got != session {
		t.Fatal("owning agent instance could not route its session")
	}
	if got := GetSessionForAgentInstance(session.ID, foreign); got != nil {
		t.Fatal("foreign agent instance routed another connection's session")
	}
}

func TestCloseAgentInstanceSessionsDoesNotCloseReplacementSessions(t *testing.T) {
	oldInstance := uuid.New()
	newInstance := uuid.New()
	oldSession := newQueueTestSession(t, nil, 1024)
	newSession := newQueueTestSession(t, nil, 1024)
	oldSession.AgentInstanceID = oldInstance
	newSession.AgentInstanceID = newInstance

	CloseAgentInstanceSessions(oldInstance)

	if GetSession(oldSession.ID) != nil {
		t.Fatal("disconnected agent instance retained its session")
	}
	if GetSession(newSession.ID) == nil {
		t.Fatal("stale disconnect closed replacement agent session")
	}
}

func TestSessionCloseRetainsTeardownSafeAuditRoute(t *testing.T) {
	s := newQueueTestSession(t, nil, 1024)
	s.AgentInstanceID = uuid.New()
	storeSessionAuditRoute(s.ID, "database-session", "org-id", s.AgentInstanceID)
	t.Cleanup(func() { deleteSessionAuditRoute(s.ID) })

	s.Close()
	if live := GetSession(s.ID); live != nil {
		t.Fatal("closed session remained in live routing map")
	}
	route := GetSessionAuditRoute(s.ID)
	if route == nil {
		t.Fatal("audit route was deleted with live session")
	}
	if route.DatabaseSessionID != "database-session" ||
		route.OrgID != "org-id" ||
		route.AgentInstanceID != s.AgentInstanceID {
		t.Fatalf("unexpected audit route: %#v", route)
	}
}
