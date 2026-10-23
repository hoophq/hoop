package proxy

import (
	"context"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/hoophq/hoop/sidecar/telemetry"
)

// beginSession opens the connection's exchange span, or returns ctx and a
// no-op when the lane exports nothing.
func (s *Server) beginSession(ctx context.Context, sess *session.Session) (context.Context, telemetry.EndExchange) {
	if s.cfg.Telemetry == nil {
		return ctx, endNothing
	}
	return s.cfg.Telemetry.BeginExchange(ctx, telemetry.ExchangeSession,
		telemetry.Attr{Key: "hoop.session_id", Value: string(sess.ID)},
		telemetry.Attr{Key: "hoop.principal", Value: sess.Identity.Principal()},
	)
}

func endNothing(error) {}

// upstreamClock measures the upstream's share of a statement's latency on
// the byte path: from the client pump forwarding a request to the server
// pump decoding its answer.
//
// The byte path has no exact pairing of responses to requests. The codecs
// correlate them internally (pgwire pipelines, MongoDB answers by requestID)
// but do not expose the pairing on the Statement, so the clock measures
// what it can measure honestly, and says which:
//
//   - HTTP/1 answers requests in order, so a FIFO of send times pairs them
//     exactly (the same order the gate's settle queue relies on).
//   - Every other protocol is sampled only when ONE request is outstanding.
//     A second request forwarded before the answer arrives drops the
//     sample: the answer could be either's, and a number that might be
//     wrong is worse than no number. The series therefore describes
//     unpipelined exchanges, which is what the pairing label says.
//
// A nil clock, which is every lane without telemetry, records nothing.
type upstreamClock struct {
	rec   telemetry.LaneRecorder
	exact bool

	mu sync.Mutex
	// exact mode: one send time per request, answered in order.
	queue []time.Time
	// unpipelined mode: how many requests await an answer, and when the
	// first of them left.
	outstanding int
	sentAt      time.Time
}

func newUpstreamClock(proto inspect.Protocol, rec telemetry.LaneRecorder) *upstreamClock {
	if rec == nil {
		return nil
	}
	return &upstreamClock{rec: rec, exact: proto == inspect.HTTP}
}

// sent stamps n request statements the client pump just forwarded.
func (c *upstreamClock) sent(n int) {
	if c == nil || n <= 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exact {
		for range n {
			c.queue = append(c.queue, now)
		}
		return
	}
	if c.outstanding == 0 {
		c.sentAt = now
	}
	c.outstanding += n
}

// answered records n response statements the server pump just decoded.
// ctx is the connection's, so a sampled trace gets the measurement as an
// event on the session span.
func (c *upstreamClock) answered(ctx context.Context, n int) {
	if c == nil || n <= 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exact {
		for ; n > 0 && len(c.queue) > 0; n-- {
			c.rec.Upstream(ctx, now.Sub(c.queue[0]), telemetry.PairingExact)
			c.queue = c.queue[1:]
		}
		return
	}
	switch c.outstanding {
	case 0:
		// A further frame of an answer already measured, or a server
		// statement with no request behind it (a notice, an auth step).
		return
	case 1:
		c.rec.Upstream(ctx, now.Sub(c.sentAt), telemetry.PairingUnpipelined)
	}
	// Pipelined: the sample is dropped, and the clock waits for the next
	// quiet exchange.
	c.outstanding = 0
}
