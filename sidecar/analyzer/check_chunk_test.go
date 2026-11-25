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
	server(t, g, myOK(1))

	// COM_STMT_CLOSE has no reply and is never checked, but reads nothing:
	// the query after it in the same write is checked.
	client(t, g, append(myPacket(0, []byte{0x19, 7, 0, 0, 0}), myPacket(0, append([]byte{0x03}, "SELECT * FROM big WHERE v = 3"...))...))
	if side.n != 2 {
		t.Fatalf("%d checks after COM_STMT_CLOSE and a query in one write, want 2", side.n)
	}
	server(t, g, myOK(1))

	// The check would read big in the database before the USE.
	client(t, g, myPacket(0, append([]byte{0x03}, "USE other; SELECT * FROM big WHERE v = 4"...)))
	if side.n != 2 {
		t.Fatalf("a chunk that changes the database got a check")
	}
}
