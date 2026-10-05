package gate_test

import (
	"context"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
)

const reservedRequest = "GET /.well-known/hoop/reviews/x HTTP/1.1\r\nHost: h\r\n\r\n"

// answeringGate owns /.well-known/hoop/reviews/x and counts the replies it
// rendered, which is where the lane would read the plane.
func answeringGate(t *testing.T) (*gate.Gate, *recordingSink, *int) {
	t.Helper()
	sink := &recordingSink{}
	rendered := 0
	g, err := gate.New(newSession(), gate.Config{
		Protocol: inspect.HTTP,
		Audit:    sink,
		Answer: func(stmt inspect.Statement) func(context.Context) []byte {
			if stmt.HTTP.Path != "/.well-known/hoop/reviews/x" {
				return nil
			}
			return func(context.Context) []byte { rendered++; return []byte("reply") }
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g, sink, &rendered
}

// The connection's first request is answered by the lane and never forwarded.
func TestAReservedRouteIsAnsweredAsTheFirstRequest(t *testing.T) {
	g, _, _ := answeringGate(t)
	d := g.Request(context.Background(), []byte(reservedRequest))
	if string(d.Reply) != "reply" || d.Hangup || d.Allowed || len(d.Payload) != 0 {
		t.Errorf("decision = allowed %v hangup %v reply %q payload %d bytes, want the reply only",
			d.Allowed, d.Hangup, d.Reply, len(d.Payload))
	}
}

// HTTP/1.1 pairs responses with requests by order, so a reserved route behind
// another request is never answered: the reply could overtake the earlier
// response. The connection ends unanswered, nothing in the chunk is judged,
// and the plane is never read.
func TestAReservedRouteBehindAnotherRequestHangsUp(t *testing.T) {
	t.Run("same chunk", func(t *testing.T) {
		g, sink, rendered := answeringGate(t)
		d := g.Request(context.Background(), []byte("GET /api/x HTTP/1.1\r\nHost: h\r\n\r\n"+reservedRequest))
		if !d.Hangup || d.Reply != nil || d.Allowed || len(d.Payload) != 0 {
			t.Errorf("decision = allowed %v hangup %v reply %q, want a hangup", d.Allowed, d.Hangup, d.Reply)
		}
		if ev := sink.find(audit.KindStatement); ev != nil {
			t.Errorf("the chunk recorded %q as a statement, but nothing in it ran", ev.Statement)
		}
		if *rendered != 0 {
			t.Error("the plane was read for a reply that was never sent")
		}
	})
	t.Run("later chunk", func(t *testing.T) {
		g, _, rendered := answeringGate(t)
		if d := g.Request(context.Background(), []byte("GET /api/x HTTP/1.1\r\nHost: h\r\n\r\n")); !d.Allowed {
			t.Fatalf("the first request was not forwarded: %+v", d)
		}
		d := g.Request(context.Background(), []byte(reservedRequest))
		if !d.Hangup || d.Reply != nil || *rendered != 0 {
			t.Errorf("decision = hangup %v reply %q rendered %d, want a hangup and no plane read",
				d.Hangup, d.Reply, *rendered)
		}
	})
}
