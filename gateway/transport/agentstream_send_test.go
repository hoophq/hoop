package transport

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	"google.golang.org/grpc"
)

// serialStream is the gRPC side of an agent stream. It fails the test when
// two sends run in it at once, which gRPC forbids.
type serialStream struct {
	grpc.ServerStream
	t      *testing.T
	inside atomic.Int32
	sent   atomic.Int64
}

func (s *serialStream) Context() context.Context { return context.Background() }

func (s *serialStream) Send(*pb.Packet) error { return s.enter() }

func (s *serialStream) SendMsg(any) error { return s.enter() }

func (s *serialStream) enter() error {
	if n := s.inside.Add(1); n > 1 {
		s.t.Errorf("%d sends ran in the stream at once", n)
	}
	time.Sleep(20 * time.Microsecond)
	s.inside.Add(-1)
	s.sent.Add(1)
	return nil
}

func (s *serialStream) Recv() (*pb.Packet, error) { select {} }

// Flag updates go out on their own goroutines now, on every replica. They
// must not overlap the packets the proxies forward to the same agent.
func TestAgentStreamSerializesFlagUpdatesAndForwardedPackets(t *testing.T) {
	raw := &serialStream{t: t}
	agent := streamclient.NewAgent(models.Agent{ID: "agent-1", OrgID: "org-1", Name: "agent-1"}, raw)
	delivery := NewFeatureFlagDelivery(
		func(string) []FeatureFlagStream { return []FeatureFlagStream{agent} },
		func(string) map[string]bool { return map[string]bool{"f": true} })

	const forwarders, packets, updates = 4, 200, 200
	var wg sync.WaitGroup
	for range forwarders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// What ProxyStream.SendToAgent does once it found the stream.
			for range packets {
				if err := agent.Send(&pb.Packet{Type: pbagent.SessionClose}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range updates {
			delivery.Notify("org-1")
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range packets {
			_ = agent.SendMsg(&pb.Packet{})
		}
	}()
	wg.Wait()

	// The last flag send can still be running.
	deadline := time.Now().Add(2 * time.Second)
	for {
		delivery.mu.Lock()
		idle := len(delivery.inFlight) == 0
		delivery.mu.Unlock()
		if idle || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := raw.sent.Load(); got <= forwarders*packets+packets {
		t.Fatalf("sent %d packets, want the forwarded ones plus at least one flag update", got)
	}
}
