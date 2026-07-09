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
		spaceFreed:          make(chan struct{}, 1),
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

// TestForwardToTCPByteBudgetBackpressure verifies that a producer pushing
// against a stalled consumer blocks once the byte budget is reached instead
// of queueing without bound, and that queued bytes never exceed the budget.
func TestForwardToTCPByteBudgetBackpressure(t *testing.T) {
	const budget = 64 * 1024
	const msgSize = 16 * 1024

	client := &gatedClientConn{allow: make(chan struct{})}
	s := newQueueTestSession(t, client, budget)

	go s.ForwardToClient()

	var pushed atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 32 { // 512 KiB offered, 8x the budget
			s.ForwardToTCP(make([]byte, msgSize))
			pushed.Add(1)
		}
	}()

	// With the consumer fully stalled the producer must stop at the budget.
	// One message may additionally be parked inside the consumer's Send.
	time.Sleep(200 * time.Millisecond)
	if got := s.queuedBytesNow(); got > budget {
		t.Fatalf("queued bytes exceeded budget: got=%d budget=%d", got, budget)
	}
	if p := pushed.Load(); p >= 32 {
		t.Fatalf("producer was never backpressured: pushed all %d messages against a stalled consumer", p)
	}

	// Un-stall the consumer; everything must drain and the producer finish.
	close(client.allow)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("producer did not finish after consumer drained")
	}

	// Wait for the consumer to drain the tail.
	deadline := time.Now().Add(5 * time.Second)
	for client.received.Load() != 32*msgSize {
		if time.Now().After(deadline) {
			t.Fatalf("consumer drained %d bytes, want %d", client.received.Load(), 32*msgSize)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestForwardToTCPUnblocksOnClose verifies a producer blocked on the byte
// budget returns promptly when the session closes rather than leaking.
func TestForwardToTCPUnblocksOnClose(t *testing.T) {
	client := &gatedClientConn{allow: make(chan struct{})} // consumer never runs
	s := newQueueTestSession(t, client, 1024)

	s.ForwardToTCP(make([]byte, 1024)) // fills the budget exactly

	blocked := make(chan struct{})
	go func() {
		defer close(blocked)
		s.ForwardToTCP(make([]byte, 512)) // must block: budget full
	}()

	time.Sleep(50 * time.Millisecond)
	select {
	case <-blocked:
		t.Fatal("producer was not blocked by a full budget")
	default:
	}

	s.Close()
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("producer still blocked after session close")
	}
}

// TestForwardToTCPOversizedMessage verifies a single message larger than the
// whole budget is admitted when the queue is empty (no deadlock).
func TestForwardToTCPOversizedMessage(t *testing.T) {
	client := &gatedClientConn{allow: make(chan struct{})}
	close(client.allow)
	s := newQueueTestSession(t, client, 1024)

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.ForwardToTCP(make([]byte, 64*1024)) // 64x the budget
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("oversized message deadlocked the relay")
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
	go s.ForwardToTCP(bytes.Clone(payload))

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

// TestSessionConnWrapperEOFAfterClose verifies reads return io.EOF once the
// session closes and the queue is drained.
func TestSessionConnWrapperEOFAfterClose(t *testing.T) {
	s := newQueueTestSession(t, &gatedClientConn{allow: make(chan struct{})}, maxQueuedBytes)
	conn := s.ToConn()

	s.ForwardToTCP([]byte("tail"))
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

// TestSessionConnWrapperDeadline verifies the read deadline still fires.
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
}

// TestForwardToTCPConcurrentClose hammers Close against in-flight producers;
// under the previous implementation this raced into a send-on-closed-channel
// panic.
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
					s.ForwardToTCP(make([]byte, 128))
				}
			}()
		}
		time.Sleep(time.Millisecond)
		s.Close()
		wg.Wait()
	}
}
