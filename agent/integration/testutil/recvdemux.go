//go:build integration

package testutil

import (
	"context"
	"sync"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
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
	// sessionCloseSubs maps sessionID → channels that are closed when a
	// SessionClose for that session is observed. Each channel is closed
	// exactly once; see SessionCloseChan.
	sessionCloseSubs map[string][]chan struct{}
	// sessionCloseReasons records the payload of the first observed
	// SessionClose per sessionID, so tests can surface WHY the agent
	// ended a session (e.g. the OSS libhoop stub's "missing protocol
	// hoop library") instead of hanging or failing opaquely. Entries are
	// never evicted — a demux lives for one test and holds one short
	// string per closed session.
	sessionCloseReasons map[string]string
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
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
		tr:                  tr,
		conns:               make(map[string]chan *pb.Packet),
		sessionCh:           make(chan *pb.Packet, 256),
		sessionCloseSubs:    make(map[string][]chan struct{}),
		sessionCloseReasons: make(map[string]string),
		ctx:                 ctx,
		cancel:              cancel,
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
				// Session-level packet: route to sessionCh and, for
				// SessionClose packets, also signal any registered subscribers
				// so inbound pumps can unblock blocked drivers.
				if pkt.Type == pbclient.SessionClose {
					sid := string(pkt.Spec[pb.SpecGatewaySessionID])
					d.fireSessionClose(sid, string(pkt.Payload))
				}
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

// fireSessionClose records the close reason and closes all channels
// registered for the given sessionID. Safe to call from the demux loop
// (holds mu briefly). Only the first reason is kept.
func (d *RecvDemux) fireSessionClose(sessionID, reason string) {
	d.mu.Lock()
	if _, seen := d.sessionCloseReasons[sessionID]; !seen {
		d.sessionCloseReasons[sessionID] = reason
	}
	subs := d.sessionCloseSubs[sessionID]
	delete(d.sessionCloseSubs, sessionID)
	d.mu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
}

// SessionCloseReason returns the payload of the first SessionClose observed
// for sessionID, and whether one was observed at all.
func (d *RecvDemux) SessionCloseReason(sessionID string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	reason, ok := d.sessionCloseReasons[sessionID]
	return reason, ok
}

// SessionCloseChan returns a channel that is closed when the demux observes a
// SessionClose packet for sessionID. Callers can select on it to detect agent
// session teardown without consuming from the shared sessionCh.
//
// Multiple calls for the same sessionID each get their own channel — all are
// closed when the SessionClose arrives. If a SessionClose for sessionID has
// already been processed, the returned channel is closed immediately.
func (d *RecvDemux) SessionCloseChan(sessionID string) <-chan struct{} {
	ch := make(chan struct{})
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, closed := d.sessionCloseReasons[sessionID]; closed {
		// SessionClose already observed: honour the documented contract
		// by returning an already-closed channel instead of one that
		// would never fire.
		close(ch)
		return ch
	}
	d.sessionCloseSubs[sessionID] = append(d.sessionCloseSubs[sessionID], ch)
	return ch
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
