package controller

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

func newTestPacket(i int) *pb.Packet {
	return &pb.Packet{Payload: []byte(fmt.Sprintf("pkt-%d", i))}
}

// Packets pushed before the worker starts must be handled in arrival order.
func TestPacketQueueOrdering(t *testing.T) {
	queue := &packetQueue{}
	const total = 100

	var startWorker int
	for i := 0; i < total; i++ {
		start, overflow := queue.push(newTestPacket(i))
		if overflow {
			t.Fatalf("push %d overflowed unexpectedly", i)
		}
		if start {
			startWorker++
		}
	}
	if startWorker != 1 {
		t.Fatalf("expected exactly one push to request a worker, got %d", startWorker)
	}

	var handled []string
	queue.drain(func(pkt *pb.Packet) { handled = append(handled, string(pkt.Payload)) })

	if len(handled) != total {
		t.Fatalf("expected %d handled packets, got %d", total, len(handled))
	}
	for i, payload := range handled {
		if want := fmt.Sprintf("pkt-%d", i); payload != want {
			t.Fatalf("packet %d handled out of order: got %s, want %s", i, payload, want)
		}
	}
}

// After a drain empties the queue and exits, the next push must request a new
// worker; pushes while a worker is running must not.
func TestPacketQueueWorkerRespawn(t *testing.T) {
	queue := &packetQueue{}

	if start, _ := queue.push(newTestPacket(0)); !start {
		t.Fatal("first push on an idle queue must request a worker")
	}
	if start, _ := queue.push(newTestPacket(1)); start {
		t.Fatal("push before the worker ran must not request a second worker")
	}
	queue.drain(func(*pb.Packet) {})

	if start, _ := queue.push(newTestPacket(2)); !start {
		t.Fatal("push after the worker exited must request a new worker")
	}
	queue.drain(func(*pb.Packet) {})
}

// Under concurrent push/drain cycles from a single producer (mirroring the
// recv loop), every packet is handled exactly once, in order, and no two
// handler invocations overlap.
func TestPacketQueueSingleWorkerNoLoss(t *testing.T) {
	queue := &packetQueue{}
	const total = 1000

	var (
		wg         sync.WaitGroup
		inHandler  atomic.Int32
		handledMu  sync.Mutex
		handled    []string
		overlapped atomic.Bool
	)
	handle := func(pkt *pb.Packet) {
		if inHandler.Add(1) > 1 {
			overlapped.Store(true)
		}
		handledMu.Lock()
		handled = append(handled, string(pkt.Payload))
		handledMu.Unlock()
		inHandler.Add(-1)
	}

	for i := 0; i < total; i++ {
		start, overflow := queue.push(newTestPacket(i))
		if overflow {
			t.Fatalf("push %d overflowed unexpectedly", i)
		}
		if start {
			wg.Add(1)
			go func() {
				defer wg.Done()
				queue.drain(handle)
			}()
		}
	}
	wg.Wait()

	if overlapped.Load() {
		t.Fatal("two handler invocations overlapped: queue must serialize handling")
	}
	if len(handled) != total {
		t.Fatalf("expected %d handled packets, got %d", total, len(handled))
	}
	for i, payload := range handled {
		if want := fmt.Sprintf("pkt-%d", i); payload != want {
			t.Fatalf("packet %d handled out of order: got %s, want %s", i, payload, want)
		}
	}
}

// The packet-count bound rejects pushes once the queue holds
// maxQueuedPackets, and draining frees budget for new pushes.
func TestPacketQueueOverflowPacketCount(t *testing.T) {
	queue := &packetQueue{}
	// Consume the worker-start signal so packets just accumulate,
	// simulating a wedged drain worker.
	if start, _ := queue.push(newTestPacket(0)); !start {
		t.Fatal("first push must request a worker")
	}
	for i := 1; i < maxQueuedPackets; i++ {
		if _, overflow := queue.push(newTestPacket(i)); overflow {
			t.Fatalf("push %d overflowed below the bound", i)
		}
	}
	if _, overflow := queue.push(newTestPacket(maxQueuedPackets)); !overflow {
		t.Fatal("push past maxQueuedPackets must overflow")
	}

	// Draining frees the budget.
	queue.drain(func(*pb.Packet) {})
	if _, overflow := queue.push(newTestPacket(0)); overflow {
		t.Fatal("push after drain must not overflow")
	}
	queue.drain(func(*pb.Packet) {})
}

// The byte bound rejects a push whose payload would exceed maxQueuedBytes,
// and the byte budget is returned as packets drain.
func TestPacketQueueOverflowBytes(t *testing.T) {
	queue := &packetQueue{}
	big := &pb.Packet{Payload: make([]byte, maxQueuedBytes-1)}
	if _, overflow := queue.push(big); overflow {
		t.Fatal("first big push must fit")
	}
	if _, overflow := queue.push(&pb.Packet{Payload: make([]byte, 2)}); !overflow {
		t.Fatal("push exceeding maxQueuedBytes must overflow")
	}
	queue.drain(func(*pb.Packet) {})
	if _, overflow := queue.push(big); overflow {
		t.Fatal("byte budget must be freed by drain")
	}
	queue.drain(func(*pb.Packet) {})
}

// A blocking handler must not block producers: pushes during a stalled
// handler are queued (never lost, never reordered) and handled once the
// handler unblocks.
func TestPacketQueueBlockingHandlerStillSerializes(t *testing.T) {
	queue := &packetQueue{}
	release := make(chan struct{})
	handledCh := make(chan string, 16)

	handle := func(pkt *pb.Packet) {
		if string(pkt.Payload) == "pkt-0" {
			<-release // simulate a slow upstream dial on the first packet
		}
		handledCh <- string(pkt.Payload)
	}

	start, _ := queue.push(newTestPacket(0))
	if !start {
		t.Fatal("first push must request a worker")
	}
	done := make(chan struct{})
	go func() {
		queue.drain(handle)
		close(done)
	}()

	// Producer keeps pushing while the handler is blocked; none of these
	// may spawn a second worker or be dropped.
	for i := 1; i <= 5; i++ {
		start, overflow := queue.push(newTestPacket(i))
		if start || overflow {
			t.Fatalf("push %d during blocked handler: start=%v overflow=%v", i, start, overflow)
		}
	}

	close(release)
	for i := 0; i <= 5; i++ {
		got := <-handledCh
		if want := fmt.Sprintf("pkt-%d", i); got != want {
			t.Fatalf("handled out of order after unblock: got %s want %s", got, want)
		}
	}
	<-done
}
