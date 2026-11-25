package analyzer_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

func myPacket(seq uint8, payload []byte) []byte {
	n := len(payload)
	return append([]byte{byte(n), byte(n >> 8), byte(n >> 16), seq}, payload...)
}

func myOK(seq uint8) []byte {
	return myPacket(seq, []byte{0x00, 0, 0, 0x02, 0x00, 0, 0})
}

func myEOF(seq uint8) []byte {
	return myPacket(seq, []byte{0xFE, 0, 0, 0x02, 0x00})
}

func myLenencStr(b []byte, s string) []byte {
	return append(append(b, byte(len(s))), s...)
}

func myColumnDef(seq uint8, name string) []byte {
	var p []byte
	for _, s := range []string{"def", "app", "customers", "customers", name, name} {
		p = myLenencStr(p, s)
	}
	p = append(p, 0x0c)
	p = binary.LittleEndian.AppendUint16(p, 33)
	p = binary.LittleEndian.AppendUint32(p, 11)
	p = append(p, 0x03, 0, 0, 0, 0, 0)
	return myPacket(seq, p)
}

// mysqlGate is a gate on the real mysql codec, past the handshake.
func mysqlGate(t *testing.T, ev *analyzer.Evaluator, sink audit.Sink) *gate.Gate {
	t.Helper()
	sess := session.New(inspect.MySQL, session.Identity{Subject: "alice@example.com"})
	g, err := gate.New(sess, gate.Config{Protocol: inspect.MySQL, Policy: ev, Audit: sink})
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	var greeting bytes.Buffer
	greeting.WriteByte(10)
	greeting.WriteString("8.0.35\x00")
	greeting.Write(make([]byte, 33))
	var resp bytes.Buffer
	_ = binary.Write(&resp, binary.LittleEndian, uint32(1<<9))
	_ = binary.Write(&resp, binary.LittleEndian, uint32(1<<24))
	resp.WriteByte(33)
	resp.Write(make([]byte, 23))
	resp.WriteString("app\x00")

	server(t, g, myPacket(0, greeting.Bytes()))
	client(t, g, myPacket(1, resp.Bytes()))
	server(t, g, myOK(2))
	return g
}

func client(t *testing.T, g *gate.Gate, b []byte) gate.Decision {
	t.Helper()
	d := g.Request(context.Background(), b)
	if !d.Allowed {
		t.Fatalf("client packet denied: %+v", d)
	}
	return d
}

func server(t *testing.T, g *gate.Gate, b []byte) {
	t.Helper()
	if d := g.Response(context.Background(), b); !d.Allowed {
		t.Fatalf("server packet denied: %+v", d)
	}
}

type statementSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (s *statementSink) Write(_ context.Context, ev audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.Kind == audit.KindStatement && ev.Direction == inspect.FromClient {
		s.events = append(s.events, ev)
	}
	return nil
}

func (s *statementSink) Close() error { return nil }

// A MySQL prepared statement sends one text on COM_STMT_PREPARE, each
// COM_STMT_EXECUTE and COM_STMT_CLOSE. With no verdict cache, the model sees
// it once: the executes reuse the prepare's verdict and the close is not
// classified.
func TestPreparedStatementMakesOneCall(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskMedium}
	ev := mustNew(t, analyzer.Config{
		Rule:     "risk",
		Provider: p,
		Trigger:  analyzer.Trigger{All: true},
		Actions:  analyzer.ActionMap{analyzer.RiskMedium: analyzer.ActionWarn},
	})
	sink := &statementSink{}
	g := mysqlGate(t, ev, sink)

	client(t, g, myPacket(0, append([]byte{0x16}, "SELECT name FROM customers WHERE id = ?"...)))
	server(t, g, myPacket(1, []byte{0x00, 7, 0, 0, 0, 1, 0, 1, 0, 0, 0, 0}))
	server(t, g, myColumnDef(2, "id"))
	server(t, g, myEOF(3))
	server(t, g, myColumnDef(4, "name"))
	server(t, g, myEOF(5))

	execute := myPacket(0, []byte{0x17, 7, 0, 0, 0, 0x00, 1, 0, 0, 0})
	for range 3 {
		client(t, g, execute)
		server(t, g, myOK(1))
	}
	client(t, g, myPacket(0, []byte{0x19, 7, 0, 0, 0}))

	if got := p.calls.Load(); got != 1 {
		t.Fatalf("the model was called %d times for one prepared statement, want 1", got)
	}
	if len(sink.events) != 5 {
		t.Fatalf("recorded %d client statements, want 5", len(sink.events))
	}
	for i, want := range []string{"ok", "cached", "cached", "cached", ""} {
		md := sink.events[i].Metadata
		if got := md[analyzer.MetadataAIStatus]; got != want {
			t.Errorf("statement %d (%s): ai_status = %q, want %q", i, md["mysql.command"], got, want)
		}
		if want != "" && md[analyzer.MetadataRiskLevel] != "medium" {
			t.Errorf("statement %d (%s): risk_level = %q, want medium", i, md["mysql.command"], md[analyzer.MetadataRiskLevel])
		}
	}
}

// The memo belongs to one connection: another connection preparing the same
// text asks the model again when the lane has no verdict cache.
func TestPreparedMemoIsPerConnection(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskLow}
	ev := mustNew(t, analyzer.Config{Provider: p, Trigger: analyzer.Trigger{All: true}, Actions: analyzer.ActionMap{analyzer.RiskLow: analyzer.ActionAllow}})
	for range 2 {
		g := mysqlGate(t, ev, nil)
		client(t, g, myPacket(0, append([]byte{0x16}, "DELETE FROM customers WHERE id = 1"...)))
		server(t, g, myPacket(1, []byte{0x00, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}))
		client(t, g, myPacket(0, []byte{0x17, 7, 0, 0, 0, 0x00, 1, 0, 0, 0}))
	}
	if got := p.calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want one per connection", got)
	}
}

// The memo keys on the exact text: two texts of one shape each ask the model
// when the lane has no verdict cache.
func TestPreparedMemoKeysOnTheText(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskLow}
	ev := mustNew(t, analyzer.Config{Provider: p, Trigger: analyzer.Trigger{All: true}, Actions: analyzer.ActionMap{analyzer.RiskLow: analyzer.ActionAllow}})
	g := mysqlGate(t, ev, nil)
	for i, text := range []string{"SELECT * FROM t1 WHERE id = ?", "SELECT * FROM t2 WHERE id = ?", "SELECT * FROM t1 WHERE id = ?"} {
		client(t, g, myPacket(0, append([]byte{0x16}, text...)))
		server(t, g, myPacket(1, []byte{0x00, byte(i + 1), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}))
	}
	if got := p.calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want one per text", got)
	}
}

// The hand-off agent's decision rides with the memo: an execute asks neither
// the classifier nor the agent again.
func TestPreparedRunReusesTheHandoffDecision(t *testing.T) {
	p := &handoffProvider{replies: []decideReply{pick("warn", "scoped by id")}}
	ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: allFour}, nil)
	g := mysqlGate(t, ev, nil)

	client(t, g, myPacket(0, append([]byte{0x16}, "DELETE FROM customers WHERE id = ?"...)))
	server(t, g, myPacket(1, []byte{0x00, 7, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0}))
	server(t, g, myColumnDef(2, "id"))
	server(t, g, myEOF(3))
	for range 2 {
		client(t, g, myPacket(0, []byte{0x17, 7, 0, 0, 0, 0x00, 1, 0, 0, 0}))
		server(t, g, myOK(1))
	}
	if got := p.calls.Load(); got != 1 {
		t.Errorf("classifier calls = %d, want 1", got)
	}
	if got := len(p.asks()); got != 1 {
		t.Errorf("hand-off agent calls = %d, want 1", got)
	}
}
