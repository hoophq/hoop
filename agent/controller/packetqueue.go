package controller

import (
	"sync"

	pb "github.com/hoophq/hoop/common/proto"
)

const (
	// maxQueuedPackets / maxQueuedBytes bound a single connection's FIFO.
	// The queue only grows while its drain worker is blocked on the
	// upstream (dial or a stalled write); a healthy connection drains as
	// fast as packets arrive. The byte cap is the meaningful one — 64 MiB
	// of buffered payload for one wedged connection is already generous —
	// and the packet cap catches pathological streams of tiny packets.
	maxQueuedPackets = 4096
	maxQueuedBytes   = 64 << 20 // 64 MiB
)

// packetQueue is a per-connection FIFO drained by at most one worker
// goroutine at a time. It is the agent's ordering primitive for packet
// types whose handling must not block the recv loop but MUST preserve
// the gRPC stream's arrival order within a connection: dispatching such
// packets with `go` breaks ordering (goroutines are scheduled
// arbitrarily, and a per-connection mutex only prevents interleaving,
// not reordering), which for SSH meant an exec request could reach the
// proxy before the OpenChannel it belongs to and be dropped.
//
// The queue is bounded (maxQueuedPackets/maxQueuedBytes): a wedged
// upstream must not let one connection buffer unbounded payload in agent
// memory. Overflow is terminal for the connection — the caller closes
// the session with an explicit error.
//
// Used by processHttpProxyWriteServer and processSSHWriteQueued.
type packetQueue struct {
	mu          sync.Mutex
	packets     []*pb.Packet
	queuedBytes int
	running     bool
}

// push appends pkt and reports whether the caller must start a drain worker.
// At most one caller observes startWorker=true until that worker exits.
// overflow=true means the packet was NOT queued because the bound was hit;
// the caller must treat the connection as failed.
func (q *packetQueue) push(pkt *pb.Packet) (startWorker, overflow bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.packets) >= maxQueuedPackets || q.queuedBytes+len(pkt.Payload) > maxQueuedBytes {
		return false, true
	}
	q.packets = append(q.packets, pkt)
	q.queuedBytes += len(pkt.Payload)
	if q.running {
		return false, false
	}
	q.running = true
	return true, false
}

// drain invokes handle on queued packets in arrival order and exits when the
// queue is empty, so an idle connection does not hold a parked goroutine. The
// running flag guarantees at most one worker per queue: it is cleared under
// the same lock that observes emptiness, so a packet pushed after the last
// drain always finds running=false and spawns a fresh worker, while a packet
// pushed mid-drain is picked up by the current one.
func (q *packetQueue) drain(handle func(*pb.Packet)) {
	for {
		q.mu.Lock()
		if len(q.packets) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		pkt := q.packets[0]
		q.packets = q.packets[1:]
		q.queuedBytes -= len(pkt.Payload)
		q.mu.Unlock()
		handle(pkt)
	}
}
