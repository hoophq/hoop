package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/telemetry"
)

// upstreamLog is a LaneRecorder that keeps only the upstream samples.
type upstreamLog struct {
	telemetry.LaneRecorder // nil: every other method panics if called
	samples                []telemetry.Pairing
}

func (l *upstreamLog) Upstream(_ context.Context, d time.Duration, p telemetry.Pairing) {
	if d < 0 {
		panic("negative duration")
	}
	l.samples = append(l.samples, p)
}

// On a protocol whose codec does not pair responses to requests, the clock
// samples only an exchange with one request outstanding. Two requests in
// flight drop the sample rather than guess which one the answer belongs to,
// and a further frame of an answer already measured records nothing.
func TestUpstreamClockSamplesOnlyUnpipelinedExchanges(t *testing.T) {
	log := &upstreamLog{}
	c := newUpstreamClock(inspect.Postgres, log)
	ctx := context.Background()

	c.sent(1)
	c.answered(ctx, 1) // one outstanding: sampled
	c.answered(ctx, 2) // more frames of the same answer: nothing
	if len(log.samples) != 1 || log.samples[0] != telemetry.PairingUnpipelined {
		t.Fatalf("samples after a quiet exchange = %v, want [unpipelined]", log.samples)
	}

	c.sent(1)
	c.sent(1) // pipelined before the answer
	c.answered(ctx, 1)
	if len(log.samples) != 1 {
		t.Fatalf("a pipelined exchange was sampled: %v", log.samples)
	}

	c.sent(2) // two statements in one chunk are two requests
	c.answered(ctx, 1)
	if len(log.samples) != 1 {
		t.Fatalf("a two-statement chunk was sampled: %v", log.samples)
	}

	c.sent(1) // the clock recovers on the next quiet exchange
	c.answered(ctx, 1)
	if len(log.samples) != 2 {
		t.Fatalf("the clock did not recover after pipelining: %v", log.samples)
	}
}

// HTTP/1 answers in order, so every request gets an exact sample, pipelined
// or not, and an answer with no request behind it records nothing.
func TestUpstreamClockPairsHTTPByOrder(t *testing.T) {
	log := &upstreamLog{}
	c := newUpstreamClock(inspect.HTTP, log)
	ctx := context.Background()

	c.sent(2)
	c.answered(ctx, 1)
	c.answered(ctx, 1)
	c.answered(ctx, 1) // nothing left to pair
	if len(log.samples) != 2 {
		t.Fatalf("samples = %v, want two exact", log.samples)
	}
	for _, p := range log.samples {
		if p != telemetry.PairingExact {
			t.Errorf("pairing = %s, want exact", p)
		}
	}
}

// A lane without telemetry has no clock, and the pumps call through nil.
func TestNilUpstreamClockIsSafe(t *testing.T) {
	var c *upstreamClock
	c.sent(1)
	c.answered(context.Background(), 1)
	if newUpstreamClock(inspect.Postgres, nil) != nil {
		t.Fatal("a lane with no recorder got a clock")
	}
}
