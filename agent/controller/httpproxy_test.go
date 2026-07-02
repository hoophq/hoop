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
func TestHttpProxyPacketQueueOrdering(t *testing.T) {
	queue := &httpProxyPacketQueue{}
	const total = 100

	var startWorker int
	for i := 0; i < total; i++ {
		if queue.push(newTestPacket(i)) {
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
func TestHttpProxyPacketQueueWorkerRespawn(t *testing.T) {
	queue := &httpProxyPacketQueue{}

	if !queue.push(newTestPacket(0)) {
		t.Fatal("first push on an idle queue must request a worker")
	}
	if queue.push(newTestPacket(1)) {
		t.Fatal("push before the worker ran must not request a second worker")
	}
	queue.drain(func(*pb.Packet) {})

	if !queue.push(newTestPacket(2)) {
		t.Fatal("push after the worker exited must request a new worker")
	}
	queue.drain(func(*pb.Packet) {})
}

// Under concurrent push/drain cycles from a single producer (mirroring the
// recv loop), every packet is handled exactly once, in order, and no two
// handler invocations overlap.
func TestHttpProxyPacketQueueSingleWorkerNoLoss(t *testing.T) {
	queue := &httpProxyPacketQueue{}
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
		if queue.push(newTestPacket(i)) {
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
