package gate_test

import (
	"context"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// A reserved route pipelined behind another request is answered before either
// is judged. Judging the first would audit it as run, and the reply closes
// the connection before it could be forwarded.
func TestAReservedRouteIsAnsweredBeforeTheChunkIsJudged(t *testing.T) {
	sink := &recordingSink{}
	g, err := gate.New(newSession(), gate.Config{
		Protocol: inspect.HTTP,
		Audit:    sink,
		Answer: func(_ context.Context, stmt inspect.Statement) []byte {
			if stmt.HTTP.Path == "/.well-known/hoop/reviews/x" {
				return []byte("reply")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	d := g.Request(context.Background(), []byte(
		"GET /api/x HTTP/1.1\r\nHost: h\r\n\r\n"+
			"GET /.well-known/hoop/reviews/x HTTP/1.1\r\nHost: h\r\n\r\n"))

	if string(d.Reply) != "reply" || d.Allowed || len(d.Payload) != 0 {
		t.Fatalf("decision = allowed %v reply %q payload %d bytes, want the reply and nothing forwarded",
			d.Allowed, d.Reply, len(d.Payload))
	}
	if ev := sink.find(audit.KindStatement); ev != nil {
		t.Errorf("the chunk recorded %q as a statement, but nothing in it ran", ev.Statement)
	}
}
