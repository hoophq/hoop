package controller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hoophq/hoop/agent/config"
	pb "github.com/hoophq/hoop/common/proto"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
)

// scriptedTransport hands Run the packets of one gateway stream, then ends it.
type scriptedTransport struct {
	pkts   []*pb.Packet
	onRecv func()
	ctx    context.Context
}

func (t *scriptedTransport) Recv() (*pb.Packet, error) {
	if t.onRecv != nil {
		t.onRecv()
	}
	if len(t.pkts) == 0 {
		return nil, errors.New("stream closed")
	}
	pkt := t.pkts[0]
	t.pkts = t.pkts[1:]
	return pkt, nil
}
func (t *scriptedTransport) Send(*pb.Packet) error          { return nil }
func (t *scriptedTransport) StreamContext() context.Context { return t.ctx }
func (t *scriptedTransport) StartKeepAlive()                {}
func (t *scriptedTransport) Close() (error, error)          { return nil, nil }

// A reconnect to a gateway that sends no flag snapshot must not inherit the
// flags of the previous stream, and the new stream must not change the
// policy that work of the old controller still reads (DEP-289).
func TestFeatureFlagsArePerGatewayStream(t *testing.T) {
	const flag = "experimental.ssh_input_guardrails"
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stop the log shipper right away

	raw, _ := json.Marshal(map[string]bool{flag: true})
	old := New(&scriptedTransport{ctx: ctx, pkts: []*pb.Packet{{
		Type: pbgateway.FeatureFlagUpdate,
		Spec: map[string][]byte{pb.SpecFeatureFlagsKey: raw},
	}}}, &config.Config{}, nil)
	_ = old.Run()
	if !old.flags.IsEnabled(flag) {
		t.Fatalf("first stream: %s must be on after its snapshot", flag)
	}

	enabledAtStart := true
	second := &scriptedTransport{ctx: ctx}
	next := New(second, &config.Config{}, nil)
	second.onRecv = func() {
		enabledAtStart = next.flags.IsEnabled(flag)
		second.onRecv = nil
	}
	_ = next.Run()
	if enabledAtStart || next.flags.IsEnabled(flag) {
		t.Fatalf("second stream sent no snapshot: %s must be off", flag)
	}
	if !old.flags.IsEnabled(flag) {
		t.Fatalf("old controller: %s must keep the policy of its own stream", flag)
	}
}
