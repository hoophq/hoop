//go:build integration

package testutil

import (
	"context"
	"sync"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
)

// RecvDemux fans out the agent's outgoing packets from a MockTransport into
// per-connection-ID channels so concurrent tests can drain responses
// independently for each session/connection.
//
// The serial tests in postgres_test.go drain MockTransport.RecvCh() directly,
// which is fine when only one in-flight query exists at a time. The
// concurrency tests have multiple concurrent in-flight connections sharing
// the same MockTransport; without demultiplexing they would race to read
// each other's responses out of the shared channel.
//
// Demux is started once after the agent is up. From then on, callers must
// use Demux.Channel(connID) to receive packets — direct reads from
// MockTransport.RecvCh() will steal packets from the demux.
type RecvDemux struct {
	tr    *MockTransport
	mu    sync.Mutex
	conns map[string]chan *pb.Packet
	// Packets without SpecClientConnectionID (session-level events like
	// SessionOpenOK and SessionClose) are routed here.
	sessionCh chan *pb.Packet
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// StartRecvDemux begins consuming from tr.RecvCh() in a background goroutine
// and routes each packet to the channel registered for its
// SpecClientConnectionID. Channels are created lazily on first reference.
//
// The demux runs until the test context is canceled (via Stop) or the
// MockTransport is closed.
func StartRecvDemux(t T, tr *MockTransport) *RecvDemux {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := &RecvDemux{
		tr:        tr,
		conns:     make(map[string]chan *pb.Packet),
		sessionCh: make(chan *pb.Packet, 256),
		ctx:       ctx,
		cancel:    cancel,
	}

	d.wg.Add(1)
	go d.loop()

	t.Cleanup(func() {
		d.Stop()
	})
	return d
}

func (d *RecvDemux) loop() {
	defer d.wg.Done()
	for {
		select {
		case <-d.ctx.Done():
			return
		case pkt, ok := <-d.tr.RecvCh():
			if !ok {
				return
			}
			connID := string(pkt.Spec[pb.SpecClientConnectionID])
			if connID == "" {
				select {
				case d.sessionCh <- pkt:
				case <-d.ctx.Done():
					return
				}
				continue
			}
			ch := d.channelFor(connID)
			select {
			case ch <- pkt:
			case <-d.ctx.Done():
				return
			}
		}
	}
}

func (d *RecvDemux) channelFor(connID string) chan *pb.Packet {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch, ok := d.conns[connID]
	if !ok {
		// Buffer generously so a busy goroutine forwarding query results
		// doesn't block the demux loop and stall siblings.
		ch = make(chan *pb.Packet, 256)
		d.conns[connID] = ch
	}
	return ch
}

// Channel returns the per-connection-ID receive channel. The channel is
// created on first call and shared on subsequent calls for the same connID.
//
// Multiple receivers on the same channel are not supported — each connID
// should be consumed by a single goroutine in the test.
func (d *RecvDemux) Channel(connID string) <-chan *pb.Packet {
	return d.channelFor(connID)
}

// SessionChannel returns packets that don't carry a SpecClientConnectionID
// (SessionOpenOK, SessionClose, etc).
func (d *RecvDemux) SessionChannel() <-chan *pb.Packet {
	return d.sessionCh
}

// Stop cancels the demux loop and waits for it to exit.
// Idempotent.
func (d *RecvDemux) Stop() {
	d.cancel()
	d.wg.Wait()
}

// WaitForReadyOnConn drains the per-conn channel until a Postgres
// ReadyForQuery ('Z') message is observed. Mirrors WaitForPGReady but
// scoped to a specific connection.
func WaitForReadyOnConn(t T, demux *RecvDemux, connID string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	ch := demux.Channel(connID)
	for {
		select {
		case pkt, ok := <-ch:
			if !ok {
				t.Fatalf("recvdemux: channel closed before ready for conn=%s", connID)
				return
			}
			msgs := ParsePGMessages(pkt.Payload)
			for _, msg := range msgs {
				if msg.Type == byte('Z') {
					return
				}
			}
		case <-deadline:
			t.Fatalf("recvdemux: timed out waiting for ready on conn=%s after %v", connID, timeout)
			return
		}
	}
}

// CollectResponsesOnConn drains the per-conn channel until ReadyForQuery
// ('Z') is observed, returning all packets seen. Mirrors CollectPGResponses
// but scoped to a specific connection.
func CollectResponsesOnConn(t T, demux *RecvDemux, connID string, timeout time.Duration) []*pb.Packet {
	t.Helper()
	var pkts []*pb.Packet
	deadline := time.After(timeout)
	ch := demux.Channel(connID)
	for {
		select {
		case pkt, ok := <-ch:
			if !ok {
				t.Fatalf("recvdemux: channel closed before ready for conn=%s after %d packets", connID, len(pkts))
				return pkts
			}
			pkts = append(pkts, pkt)
			msgs := ParsePGMessages(pkt.Payload)
			for _, msg := range msgs {
				if msg.Type == byte('Z') {
					return pkts
				}
			}
		case <-deadline:
			t.Fatalf("recvdemux: timed out collecting responses on conn=%s after %v, got %d packets",
				connID, timeout, len(pkts))
			return pkts
		}
	}
}
