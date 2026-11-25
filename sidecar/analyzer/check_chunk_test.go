package analyzer_test

import (
	"context"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

type countingSide struct{ n int }

func (s *countingSide) Exchange(context.Context, []byte, inspect.CheckResponse, []byte) error {
	s.n++
	return policy.ErrCheckNotReady
}

// A check before a chunk runs before every statement in it, so a statement
// that reads the previous result stops the check for the whole chunk.
func TestAChunkThatReadsThePreviousResultGetsNoCheck(t *testing.T) {
	ev := mustNew(t, analyzer.Config{
		Rule:          "risk",
		Provider:      &stubProvider{level: analyzer.RiskLow},
		Trigger:       analyzer.Trigger{All: true},
		ResourceCheck: true,
	})
	g := mysqlGate(t, ev, &statementSink{})
	side := &countingSide{}
	g.AttachSidePath(side)

	client(t, g, myPacket(0, append([]byte{0x03}, "SELECT * FROM big WHERE v = 1"...)))
	server(t, g, myOK(1))
	if side.n != 1 {
		t.Fatalf("%d checks for a plain query, want 1", side.n)
	}
	client(t, g, myPacket(0, append([]byte{0x03}, "SELECT ROW_COUNT(); SELECT * FROM big WHERE v = 2"...)))
	if side.n != 1 {
		t.Fatalf("a chunk that reads ROW_COUNT() got a check")
	}
}
