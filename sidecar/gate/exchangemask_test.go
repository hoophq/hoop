package gate_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// maskingPolicy applies a response mask to every request whose text or path
// says "masked", as the AI analyzer does for a statement it masks.
type maskingPolicy struct{ m policy.ResponseMask }

func (maskingPolicy) Evaluate(inspect.Statement) policy.Verdict { return policy.Verdict{} }

func (p maskingPolicy) EvaluateWith(s inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	text := s.Text
	if s.HTTP != nil {
		text += s.HTTP.Path
	}
	if s.Direction == inspect.FromClient && strings.Contains(text, "masked") {
		ec.MaskResponse(p.m)
	}
	return policy.Verdict{}
}

var aiMask = stubMasker{find: "ada@example.com", replace: "[AI:EMAIL_ADDRESS]"}

// pgResult is one result set carrying ada@example.com, then ReadyForQuery.
func pgResult() []byte {
	desc := "T\x00\x00\x00\x1d\x00\x01mail\x00" +
		"\x00\x00\x00\x00\x00\x00\x00\x00\x00\x19\x00\x00\x00\x00\x00\x00\x00\x00"
	row := "D\x00\x00\x00\x19\x00\x01\x00\x00\x00\x0fada@example.com"
	done := "C\x00\x00\x00\x0dSELECT 1\x00"
	ready := "Z\x00\x00\x00\x05I"
	return []byte(desc + row + done + ready)
}

func scopedGate(t *testing.T, proto inspect.Protocol, scope gate.MaskScope) *gate.Gate {
	t.Helper()
	sess := session.New(proto, session.Identity{Subject: "alice"})
	g, err := gate.New(sess, gate.Config{Protocol: proto, Policy: maskingPolicy{aiMask}, MaskScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func respond(g *gate.Gate, data []byte) []byte {
	d := g.Response(context.Background(), data)
	return append(d.Payload, g.FlushResponse()...)
}

// A serial lane masks the response of the statement the AI masked, and not
// the response of the next statement on the same connection.
func TestASerialPlanMasksThisStatementsResponseOnly(t *testing.T) {
	g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
	ctx := context.Background()

	if d := g.Request(ctx, pgQuery("SELECT mail FROM users -- masked")); !d.Allowed {
		t.Fatalf("masked statement denied: %s", d.Message)
	}
	// The response in two reads: the row is held across them and still
	// belongs to the masked statement.
	res := pgResult()
	out := append(g.Response(ctx, res[:40]).Payload, respond(g, res[40:])...)
	if bytes.Contains(out, []byte("ada@example.com")) || !bytes.Contains(out, []byte("[AI:EMAIL_ADDRESS]")) {
		t.Fatalf("the masked statement's response = %q", out)
	}

	if d := g.Request(ctx, pgQuery("SELECT mail FROM users")); !d.Allowed {
		t.Fatalf("plain statement denied: %s", d.Message)
	}
	if out := respond(g, pgResult()); !bytes.Contains(out, []byte("ada@example.com")) {
		t.Errorf("the next statement's response was masked: %q", out)
	}
}

// A statement the client sends before any byte of the masked statement's
// response arrived is refused: its response could not be told apart.
func TestASerialPlanRefusesAPipelinedStatement(t *testing.T) {
	g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
	ctx := context.Background()
	g.Request(ctx, pgQuery("SELECT mail FROM users -- masked"))
	d := g.Request(ctx, pgQuery("SELECT 1"))
	if d.Allowed || !strings.Contains(d.Message, "has not ended yet") {
		t.Fatalf("pipelined statement = %+v", d)
	}
	respond(g, pgResult())
	if d := g.Request(ctx, pgQuery("SELECT 1")); !d.Allowed {
		t.Errorf("statement after the response denied: %s", d.Message)
	}
}

// A masked statement that arrived with another in one read is refused.
func TestASerialPlanRefusesAStatementSentWithOthers(t *testing.T) {
	g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
	both := append(pgQuery("SELECT 1"), pgQuery("SELECT mail FROM users -- masked")...)
	d := g.Request(context.Background(), both)
	if d.Allowed || d.Rule != "ai-mask" || !strings.Contains(d.Message, "send it alone") {
		t.Fatalf("decision = %+v", d)
	}
}

// A lane that cannot tie a response to one statement refuses the statement
// the AI masks, and allows one it does not.
func TestAnUnscopedLaneRefusesAMaskedStatement(t *testing.T) {
	pg := scopedGate(t, inspect.Postgres, gate.MaskScopeNone)
	d := pg.Request(context.Background(), pgQuery("SELECT mail FROM users -- masked"))
	if d.Allowed || d.Rule != "ai-mask" || !strings.Contains(d.Message, "cannot tie a response to one statement") {
		t.Fatalf("decision = %+v", d)
	}
	if d := pg.Request(context.Background(), pgQuery("SELECT 1")); !d.Allowed {
		t.Errorf("unmasked statement denied: %s", d.Message)
	}
}

func httpRequest(path string) []byte {
	return []byte("GET " + path + " HTTP/1.1\r\nHost: api\r\n\r\n")
}

func httpResponse() []byte {
	return []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 15\r\n\r\nada@example.com")
}

// An http lane masks the response of the masked request only, and refuses a
// request pipelined behind it.
func TestAnHTTPPlanEndsWithItsResponse(t *testing.T) {
	g := scopedGate(t, inspect.HTTP, gate.MaskScopeHTTP)
	ctx := context.Background()
	if d := g.Request(ctx, httpRequest("/masked")); !d.Allowed {
		t.Fatalf("masked request denied: %s", d.Message)
	}
	if d := g.Request(ctx, httpRequest("/next")); d.Allowed || !strings.Contains(d.Message, "has not arrived yet") {
		t.Fatalf("pipelined request = %+v", d)
	}
	if out := respond(g, httpResponse()); bytes.Contains(out, []byte("ada@example.com")) {
		t.Fatalf("masked request's response = %q", out)
	}
	if d := g.Request(ctx, httpRequest("/next")); !d.Allowed {
		t.Fatalf("next request denied: %s", d.Message)
	}
	if out := respond(g, httpResponse()); !bytes.Contains(out, []byte("ada@example.com")) {
		t.Errorf("next request's response was masked: %q", out)
	}
}

// A masked request behind an outstanding one is refused: the response that
// arrives next is the earlier request's.
func TestAnHTTPPlanRefusesARequestBehindAnOutstandingOne(t *testing.T) {
	g := scopedGate(t, inspect.HTTP, gate.MaskScopeHTTP)
	ctx := context.Background()
	g.Request(ctx, httpRequest("/first"))
	d := g.Request(ctx, httpRequest("/masked"))
	if d.Allowed || !strings.Contains(d.Message, "still waiting for its response") {
		t.Fatalf("decision = %+v", d)
	}
}

// A statement gate serves one exchange: the plan masks the call's responses
// through MaskCell, and a call without one is not masked.
func TestAStatementGatePlanCoversItsExchange(t *testing.T) {
	for _, tc := range []struct {
		text   string
		masked bool
	}{{"Users/Get masked", true}, {"Users/Get", false}} {
		g, err := gate.NewStatementGate(session.New(inspect.GRPC, session.Identity{Subject: "alice"}),
			gate.Config{Protocol: inspect.GRPC, Policy: maskingPolicy{aiMask}, MaskScope: gate.MaskScopeExchange})
		if err != nil {
			t.Fatal(err)
		}
		d := g.EvaluateStatement(context.Background(), inspect.Statement{Protocol: inspect.GRPC,
			Direction: inspect.FromClient, Text: tc.text})
		if !d.Allowed {
			t.Fatalf("%s denied: %s", tc.text, d.Message)
		}
		out, _, n := g.MaskCell("email", []byte("ada@example.com"))
		if (n == 1) != tc.masked || bytes.Contains(out, []byte("ada")) == tc.masked {
			t.Errorf("%s: MaskCell = %q, %d", tc.text, out, n)
		}
	}
}

// The byte-path scopes and the exchange scope are not interchangeable.
func TestMaskScopesMatchTheirGate(t *testing.T) {
	if _, err := gate.New(newSession(), gate.Config{Protocol: inspect.Postgres, MaskScope: gate.MaskScopeExchange}); err == nil {
		t.Error("a byte gate took the exchange scope")
	}
	if _, err := gate.NewStatementGate(newSession(), gate.Config{Protocol: inspect.GRPC, MaskScope: gate.MaskScopeSerial}); err == nil {
		t.Error("a statement gate took a byte-path scope")
	}
	if _, err := gate.New(newSession(), gate.Config{Protocol: inspect.Postgres, MaskScope: gate.MaskScopeSerial,
		CodecFactory: func() inspect.Codec { return &plainCodec{} }}); err == nil {
		t.Error("a serial scope on a codec that cannot re-frame built")
	}
	codec := &activatingCodec{}
	if _, err := gate.New(newSession(), gate.Config{Protocol: inspect.MongoDB, MaskScope: gate.MaskScopeSerial,
		CodecFactory: func() inspect.Codec { return codec }}); err != nil || !codec.enabled {
		t.Errorf("a serial scope did not turn the rewrite on: %v", err)
	}
}

// A postgres statement pipelined while the masked response is still
// arriving (after its first bytes) is refused until ReadyForQuery, and every
// row of the masked response is masked.
func TestAPostgresPlanLastsUntilReadyForQuery(t *testing.T) {
	g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
	ctx := context.Background()
	g.Request(ctx, pgQuery("SELECT mail FROM users -- masked"))
	res := pgResult()
	first := g.Response(ctx, res[:30]).Payload // RowDescription only
	if d := g.Request(ctx, pgQuery("SELECT 1")); d.Allowed || !strings.Contains(d.Message, "has not ended yet") {
		t.Fatalf("a statement pipelined into the masked response = %+v", d)
	}
	// Two rows, both of the masked statement, then its end.
	row := res[30:56]
	rest := append(append(append([]byte{}, row...), row...), res[56:]...)
	out := append(first, respond(g, rest)...)
	if bytes.Contains(out, []byte("ada@example.com")) || bytes.Count(out, []byte("[AI:EMAIL_ADDRESS]")) != 2 {
		t.Fatalf("masked response = %q", out)
	}
	if d := g.Request(ctx, pgQuery("SELECT 1")); !d.Allowed {
		t.Errorf("statement after ReadyForQuery denied: %s", d.Message)
	}
}

// wholeReadCodec stands in for a re-framing database codec: one client
// statement per read, and each server read masked as one cell.
type wholeReadCodec struct{ proto inspect.Protocol }

func (c *wholeReadCodec) Protocol() inspect.Protocol { return c.proto }
func (c *wholeReadCodec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	if dir == inspect.FromServer {
		return nil, len(data), nil
	}
	return []inspect.Statement{{Protocol: c.proto, Direction: dir, Text: string(data)}}, len(data), nil
}
func (c *wholeReadCodec) Rewrite(data []byte, mask func(string, []byte) []byte) ([]byte, inspect.ReframeResult, error) {
	if mask == nil {
		return data, inspect.ReframeResult{}, nil
	}
	out := mask("", data)
	if bytes.Equal(out, data) {
		return data, inspect.ReframeResult{}, nil
	}
	return out, inspect.ReframeResult{Cells: 1, Rows: 1}, nil
}
func (c *wholeReadCodec) Flush(func(string, []byte) []byte) []byte { return nil }

func wholeReadGate(t *testing.T, proto inspect.Protocol) *gate.Gate {
	t.Helper()
	g, err := gate.New(session.New(proto, session.Identity{Subject: "alice"}), gate.Config{
		Protocol: proto, Policy: maskingPolicy{aiMask}, MaskScope: gate.MaskScopeSerial,
		CodecFactory: func() inspect.Codec { return &wholeReadCodec{proto: proto} },
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// tdsPacket is one tabular-result packet; eom ends the server message.
func tdsPacket(body string, eom bool) []byte {
	status := byte(0)
	if eom {
		status = 1
	}
	n := 8 + len(body)
	return append([]byte{0x04, status, byte(n >> 8), byte(n), 0, 0, 1, 0}, body...)
}

// An mssql plan lasts until the packet that ends the server message: a
// statement pipelined before it is refused, and every packet of the masked
// response is masked.
func TestAnMSSQLPlanLastsUntilEndOfMessage(t *testing.T) {
	g := wholeReadGate(t, inspect.MSSQL)
	ctx := context.Background()
	g.Request(ctx, []byte("SELECT mail FROM users -- masked"))
	if out := g.Response(ctx, tdsPacket("ada@example.com", false)).Payload; bytes.Contains(out, []byte("ada")) {
		t.Fatalf("first packet = %q", out)
	}
	if d := g.Request(ctx, []byte("SELECT 1")); d.Allowed || !strings.Contains(d.Message, "has not ended yet") {
		t.Fatalf("a statement pipelined into the masked response = %+v", d)
	}
	// The EOM packet split over two reads, its header first: the end counts
	// at its last byte.
	last := tdsPacket("ada@example.com", true)
	g.Response(ctx, last[:8])
	if d := g.Request(ctx, []byte("SELECT 1")); d.Allowed {
		t.Fatal("a statement was allowed before the end-of-message packet completed")
	}
	if out := g.Response(ctx, last[8:]).Payload; bytes.Contains(out, []byte("ada@")) {
		t.Fatalf("rest of the last packet = %q", out)
	}
	if d := g.Request(ctx, []byte("SELECT 1")); !d.Allowed {
		t.Fatalf("statement after the end of message denied: %s", d.Message)
	}
	if out := g.Response(ctx, tdsPacket("ada@example.com", true)).Payload; !bytes.Contains(out, []byte("ada@example.com")) {
		t.Errorf("the next statement's response was masked: %q", out)
	}
}

// mysql, oracle and clickhouse have no end marker the gate reads: the plan
// masks every later server byte, and the connection takes no further
// statement.
func TestAPlanWithNoEndMarkerHoldsForTheConnection(t *testing.T) {
	for _, proto := range []inspect.Protocol{inspect.MySQL, inspect.Oracle, inspect.ClickHouse} {
		t.Run(string(proto), func(t *testing.T) {
			g := wholeReadGate(t, proto)
			ctx := context.Background()
			g.Request(ctx, []byte("SELECT mail FROM users -- masked"))
			for i := range 3 {
				if out := g.Response(ctx, []byte("row ada@example.com")).Payload; bytes.Contains(out, []byte("ada@")) {
					t.Fatalf("read %d of the masked response = %q", i, out)
				}
				d := g.Request(ctx, []byte("SELECT 1"))
				if d.Allowed || !strings.Contains(d.Message, "cannot see where that response ends") {
					t.Fatalf("statement %d after a masked one = %+v", i, d)
				}
			}
		})
	}
}
