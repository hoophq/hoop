package broker

import (
	"context"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// heapSampler polls HeapAlloc in the background and records the peak value
// observed. Used to measure how much memory the agent->client relay queue
// holds when the producer outruns the consumer.
type heapSampler struct {
	stop chan struct{}
	done chan struct{}
	peak uint64
}

func startHeapSampler() *heapSampler {
	s := &heapSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		var m runtime.MemStats
		ticker := time.NewTicker(500 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > s.peak {
					s.peak = m.HeapAlloc
				}
			}
		}
	}()
	return s
}

func (s *heapSampler) Stop() uint64 {
	close(s.stop)
	<-s.done
	return s.peak
}

// benchClientConn is the consumer end of the relay: a fake RDP client that
// drains at a fixed pace, emulating a WAN browser slower than the agent-side
// producer (the exact condition observed in the gateway OOM incident).
type benchClientConn struct {
	perMsgDelay time.Duration
	received    int64
}

func (c *benchClientConn) Send(data []byte) error {
	time.Sleep(c.perMsgDelay)
	c.received += int64(len(data))
	return nil
}
func (c *benchClientConn) Read() (int, []byte, error) { return 0, nil, nil }
func (c *benchClientConn) Close() error               { return nil }
func (c *benchClientConn) WrapToConnection() net.Conn { return nil }

// newBenchSession wires a Session the same way CreateRDPSession does, without
// requiring a live agent websocket.
func newBenchSession(client ConnectionCommunicator) (*Session, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID:                  uuid.New(),
		ClientCommunicator:  client,
		Protocol:            ProtocolRDP,
		dataChannel:         make(chan []byte, 1024),
		credentialsReceived: make(chan bool, 1),
		ctx:                 ctx,
		cancel:              cancel,
		maxQueueBytes:       maxQueuedBytes,
		spaceFreed:          make(chan struct{}, 1),
	}
	BrokerInstance.sessions.Store(s.ID, s)
	return s, cancel
}

// BenchmarkAgentToClientRelay reproduces the OOM scenario: the agent-side
// websocket reader pushes frames through ForwardToTCP at memory speed while
// the client drains slower. It reports the peak heap held by the queue
// (peak_MB) alongside throughput.
//
// Workload per iteration: 2048 messages x 64 KiB = 128 MiB offered.
// Consumer pace: 100us per message (~640 MB/s drain ceiling, but the producer
// is faster still, so queue growth is bounded only by the relay's policy).
func BenchmarkAgentToClientRelay(b *testing.B) {
	const (
		msgSize  = 64 * 1024
		msgCount = 2048
	)

	var peakMax uint64
	for b.Loop() {
		client := &benchClientConn{perMsgDelay: 100 * time.Microsecond}
		sess, cancel := newBenchSession(client)

		runtime.GC()
		var base runtime.MemStats
		runtime.ReadMemStats(&base)
		sampler := startHeapSampler()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess.ForwardToClient()
		}()

		for range msgCount {
			// fresh allocation per message, like websocket ReadMessage
			data := make([]byte, msgSize)
			sess.ForwardToTCP(data)
		}
		sess.Close()
		wg.Wait()

		peak := sampler.Stop()
		if peak > base.HeapAlloc {
			if delta := peak - base.HeapAlloc; delta > peakMax {
				peakMax = delta
			}
		}
		cancel()
	}

	b.SetBytes(int64(msgSize * msgCount))
	b.ReportMetric(float64(peakMax)/(1<<20), "peak_MB")
}
