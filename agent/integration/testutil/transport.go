//go:build integration

package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
)

type MockTransport struct {
	sendCh chan *pb.Packet
	recvCh chan *pb.Packet
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	closed bool
}

func NewMockTransport() *MockTransport {
	ctx, cancel := context.WithCancel(context.Background())
	return &MockTransport{
		sendCh: make(chan *pb.Packet, 100),
		recvCh: make(chan *pb.Packet, 100),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (m *MockTransport) Send(pkt *pb.Packet) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return fmt.Errorf("transport closed")
	}
	select {
	case m.sendCh <- pkt:
		return nil
	case <-m.ctx.Done():
		return m.ctx.Err()
	}
}

func (m *MockTransport) Recv() (*pb.Packet, error) {
	select {
	case pkt := <-m.recvCh:
		return pkt, nil
	case <-m.ctx.Done():
		return nil, m.ctx.Err()
	}
}

func (m *MockTransport) StreamContext() context.Context {
	return m.ctx
}

func (m *MockTransport) StartKeepAlive() {}

func (m *MockTransport) Close() (error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil
	}
	m.closed = true
	// Cancel the context but deliberately do NOT close sendCh/recvCh.
	// Long-lived helpers (the agent recv loop, the bridge pumps for the
	// DB protocols, the demux) send on these channels and only stop when
	// they observe ctx.Done(). Closing the channels here races those
	// still in-flight sends — `close()` on a channel another goroutine is
	// writing to is a data race and can panic. All readers/writers
	// already select on m.ctx.Done(), so cancellation alone unblocks
	// them; the channels are reclaimed by GC once the transport is
	// unreferenced.
	m.cancel()
	return nil, nil
}

func (m *MockTransport) Shutdown() {
	_, _ = m.Close()
}

func (m *MockTransport) Inject(pkt *pb.Packet) {
	select {
	case m.recvCh <- pkt:
	case <-m.ctx.Done():
		// Transport closed/cancelled — drop the inject rather than
		// block. Bridge pumps racing teardown rely on this.
	case <-time.After(10 * time.Second):
		panic(fmt.Sprintf("testutil: inject timed out: type=%s", pkt.Type))
	}
}

func (m *MockTransport) Expect(t T, timeout time.Duration) *pb.Packet {
	t.Helper()
	// Close() no longer closes sendCh, so a closed transport would
	// otherwise make Expect block until the timeout. Observe ctx.Done()
	// to fail fast with a clear teardown reason instead.
	select {
	case pkt := <-m.sendCh:
		return pkt
	case <-m.ctx.Done():
		t.Fatalf("testutil: transport closed while waiting for packet: %v", m.ctx.Err())
		return nil
	case <-time.After(timeout):
		t.Fatalf("testutil: expected packet, got none (timeout %v)", timeout)
		return nil
	}
}

func (m *MockTransport) ExpectType(t T, pktType string, timeout time.Duration) *pb.Packet {
	pkt := m.Expect(t, timeout)
	if pkt.Type != pktType {
		t.Fatalf("testutil: expected packet type %q, got %q", pktType, pkt.Type)
	}
	return pkt
}

func (m *MockTransport) Drain(timeout time.Duration) []*pb.Packet {
	var pkts []*pb.Packet
	deadline := time.After(timeout)
	for {
		select {
		case pkt, ok := <-m.sendCh:
			if !ok {
				return pkts
			}
			pkts = append(pkts, pkt)
		case <-deadline:
			return pkts
		}
	}
}

func (m *MockTransport) RecvCh() <-chan *pb.Packet {
	return m.sendCh
}

type T interface {
	Fatalf(format string, args ...any)
	Helper()
	Cleanup(func())
}
