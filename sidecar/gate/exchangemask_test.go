package gate_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
	libhttp "github.com/hoophq/libhoop/v2/codec/http"
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
	if proto == inspect.Postgres {
		// The gate counts responses from the first byte, so the session
		// starts as a real one does.
		ctx := context.Background()
		if d := g.Request(ctx, pgStartup()); !d.Allowed {
			t.Fatalf("startup denied: %s", d.Message)
		}
		g.Response(ctx, append([]byte("R\x00\x00\x00\x08\x00\x00\x00\x00"), pgReady...))
	}
	return g
}

var pgReady = []byte("Z\x00\x00\x00\x05I")

// pgStartup is a protocol 3.0 StartupMessage.
func pgStartup() []byte {
	params := "user\x00alice\x00\x00"
	b := binary.BigEndian.AppendUint32(nil, uint32(8+len(params)))
	b = binary.BigEndian.AppendUint32(b, 196608)
	return append(b, params...)
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

// The response limit an AI mask covers is the HTTP codec's masking limit.
func TestTheAIMaskLimitIsTheCodecLimit(t *testing.T) {
	if gate.AIMaskMaxResponseBytes != libhttp.DefaultMaxMessageBytes {
		t.Fatalf("AIMaskMaxResponseBytes = %d, codec limit %d", gate.AIMaskMaxResponseBytes, libhttp.DefaultMaxMessageBytes)
	}
}

// Review sequence: A unmasked, then B masked before A's response ended, then
// C. B is refused: A's end marker would otherwise end B's plan.
func TestAMaskedStatementBehindAnOutstandingOneIsRefused(t *testing.T) {
	t.Run("postgres", func(t *testing.T) {
		g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
		ctx := context.Background()
		if d := g.Request(ctx, pgQuery("SELECT 1")); !d.Allowed {
			t.Fatalf("A denied: %s", d.Message)
		}
		d := g.Request(ctx, pgQuery("SELECT mail FROM users -- masked"))
		if d.Allowed || !strings.Contains(d.Message, "still waiting for its response") {
			t.Fatalf("B = %+v", d)
		}
		respond(g, pgResult()) // A's response
		if d := g.Request(ctx, pgQuery("SELECT 2")); !d.Allowed {
			t.Fatalf("C denied: %s", d.Message)
		}
		if out := respond(g, pgResult()); !bytes.Contains(out, []byte("ada@example.com")) {
			t.Errorf("C's response was masked: %q", out)
		}
	})
	t.Run("more ends than requests", func(t *testing.T) {
		g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
		ctx := context.Background()
		g.Response(ctx, pgReady)
		d := g.Request(ctx, pgQuery("SELECT mail FROM users -- masked"))
		if d.Allowed || !strings.Contains(d.Message, "can no longer tell its responses apart") {
			t.Fatalf("decision = %+v", d)
		}
	})
}

// pgParse is a named Parse message.
func pgParse(name, sql string) []byte {
	body := name + "\x00" + sql + "\x00\x00\x00"
	b := append([]byte{'P'}, binary.BigEndian.AppendUint32(nil, uint32(4+len(body)))...)
	return append(b, body...)
}

var pgSync = []byte("S\x00\x00\x00\x04")

// Review sequence: Parse+Sync masked, then a plain query, then Execute. The
// masked Parse is refused: its later execution carries no statement a plan
// could follow.
func TestAMaskedPostgresParseIsRefused(t *testing.T) {
	g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
	ctx := context.Background()
	d := g.Request(ctx, append(pgParse("s1", "SELECT mail FROM users -- masked"), pgSync...))
	if d.Allowed || !strings.Contains(d.Message, "extended query protocol") {
		t.Fatalf("masked Parse = %+v", d)
	}
	if d := g.Request(ctx, pgQuery("SELECT 1")); !d.Allowed {
		t.Fatalf("plain query denied: %s", d.Message)
	}
}

// A masked read that asks for more than one response (a Query and a Sync)
// cannot say which marker is the plan's: the plan holds for the connection.
func TestASerialPlanWhoseReadAsksForSeveralResponsesHoldsForTheConnection(t *testing.T) {
	g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
	ctx := context.Background()
	// One statement and a Sync: two ReadyForQuery, one masked statement.
	if d := g.Request(ctx, append(pgQuery("SELECT mail FROM users -- masked"), pgSync...)); !d.Allowed {
		t.Fatalf("denied: %s", d.Message)
	}
	for i := range 3 {
		if out := respond(g, pgResult()); bytes.Contains(out, []byte("ada@example.com")) {
			t.Fatalf("response %d = %q", i, out)
		}
	}
	if d := g.Request(ctx, pgQuery("SELECT 1")); d.Allowed || !strings.Contains(d.Message, "reconnect") {
		t.Errorf("statement after a sticky plan = %+v", d)
	}
}

// Review sequence: masked A with a close-delimited body, then masked B. The
// codec reads the end of its buffer as the end of such a body, so A's
// response is refused before any byte leaves, and B is refused while A's
// exchange is not complete.
func TestACloseDelimitedMaskedResponseIsRefused(t *testing.T) {
	g := scopedGate(t, inspect.HTTP, gate.MaskScopeHTTP)
	ctx := context.Background()
	g.Request(ctx, httpRequest("/masked-a"))
	d := g.Response(ctx, []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nada@example.com"))
	if d.Allowed || len(d.Payload) != 0 || !strings.Contains(d.Message, "ends when the connection closes") {
		t.Fatalf("A's response = allowed %t, %q, %q", d.Allowed, d.Payload, d.Message)
	}
	if d := g.Request(ctx, httpRequest("/masked-b")); d.Allowed {
		t.Fatalf("B was admitted while A's exchange is not complete")
	}
}

// A masked response whose head does not arrive whole in one read is refused;
// an LF-only head is read as the codec reads it.
func TestAMaskedHTTPHeadIsReadWholeOrRefused(t *testing.T) {
	g := scopedGate(t, inspect.HTTP, gate.MaskScopeHTTP)
	g.Request(context.Background(), httpRequest("/masked"))
	if d := g.Response(context.Background(), []byte("HTTP/1.1 200 OK\r\nContent-Type: text/pl")); d.Allowed {
		t.Fatal("a partial head was forwarded")
	}
	g = scopedGate(t, inspect.HTTP, gate.MaskScopeHTTP)
	g.Request(context.Background(), httpRequest("/masked"))
	d := g.Response(context.Background(), []byte("HTTP/1.1 200 OK\nContent-Type: text/plain\nContent-Length: 15\n\nada@example.com"))
	out := append(d.Payload, g.FlushResponse()...)
	if !d.Allowed || bytes.Contains(out, []byte("ada@example.com")) || !bytes.Contains(out, []byte("[AI:EMAIL_ADDRESS]")) {
		t.Errorf("LF-only response: allowed %t (%s), %q", d.Allowed, d.Message, out)
	}
}

// Review sequence: masked A completes, then unmasked B sends a RowDescription
// and a DataRow, and the connection closes mid-response. B's held row is
// flushed with B's plan, which is none: it comes out unchanged.
func TestAnUnmaskedHeldRowIsFlushedUnchanged(t *testing.T) {
	g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
	ctx := context.Background()
	g.Request(ctx, pgQuery("SELECT mail FROM users -- masked"))
	if out := respond(g, pgResult()); bytes.Contains(out, []byte("ada@example.com")) {
		t.Fatalf("A = %q", out)
	}
	g.Request(ctx, pgQuery("SELECT mail FROM users"))
	res := pgResult()
	partial := res[:56] // RowDescription and one DataRow, no CommandComplete
	out := append(g.Response(ctx, partial).Payload, g.FlushResponse()...)
	if !bytes.Equal(out, partial) {
		t.Errorf("B's held row changed at the flush: %q", out)
	}
}

// Review sequence: a gzip body the codec could not unpack within its limit
// would go out as it came. A masked response that is compressed, or longer
// than the limit, is refused before any byte leaves.
func TestAMaskedHTTPResponseTheCodecMightNotMaskIsRefused(t *testing.T) {
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write([]byte(`{"email":"ada@example.com"}`))
	w.Close()
	compressed := append([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Encoding: gzip\r\n"+
		"Content-Length: "+strconv.Itoa(gz.Len())+"\r\n\r\n"), gz.Bytes()...)
	var big bytes.Buffer
	bw := gzip.NewWriter(&big)
	bw.Write([]byte(`{"email":"ada@example.com","pad":"`))
	bw.Write(bytes.Repeat([]byte("a"), gate.AIMaskMaxResponseBytes+1))
	bw.Write([]byte(`"}`))
	bw.Close()
	lfGzip := append([]byte("HTTP/1.1 200 OK\nContent-Type: application/json\nContent-Encoding: gzip\n"+
		"Content-Length: "+strconv.Itoa(big.Len())+"\n\n"), big.Bytes()...)
	long := append([]byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: "+
		strconv.Itoa(gate.AIMaskMaxResponseBytes+1)+"\r\n\r\n"), bytes.Repeat([]byte("a"), gate.AIMaskMaxResponseBytes+1)...)
	for name, tc := range map[string]struct {
		resp []byte
		want string
	}{
		"gzip":                                  {compressed, "compressed (gzip)"},
		"over limit":                            {long, "longer than"},
		"LF-only gzip inflating over the limit": {lfGzip, "compressed (gzip)"},
	} {
		t.Run(name, func(t *testing.T) {
			g := scopedGate(t, inspect.HTTP, gate.MaskScopeHTTP)
			ctx := context.Background()
			g.Request(ctx, httpRequest("/masked"))
			d := g.Response(ctx, tc.resp)
			if d.Allowed || len(d.Payload) != 0 || !strings.Contains(d.Message, tc.want) {
				t.Fatalf("decision = allowed %t, %d bytes, %q", d.Allowed, len(d.Payload), d.Message)
			}
		})
	}
	// Unmasked, the same compressed response passes as before.
	g := scopedGate(t, inspect.HTTP, gate.MaskScopeHTTP)
	g.Request(context.Background(), httpRequest("/plain"))
	if d := g.Response(context.Background(), compressed); !d.Allowed {
		t.Errorf("an unmasked compressed response was refused: %s", d.Message)
	}
}

// An AI mask on an mssql lane is denied: the daemon gives the lane no mask
// scope, because the pinned rewriter forwards some result data unmasked.
func TestAnAIMaskOnMSSQLIsDenied(t *testing.T) {
	g, err := gate.New(session.New(inspect.MSSQL, session.Identity{Subject: "alice"}), gate.Config{
		Protocol: inspect.MSSQL, Policy: maskingPolicy{aiMask}, MaskScope: gate.MaskScopeNone,
		CodecFactory: func() inspect.Codec { return &wholeReadCodec{proto: inspect.MSSQL} },
	})
	if err != nil {
		t.Fatal(err)
	}
	d := g.Request(context.Background(), []byte("SELECT mail FROM users -- masked"))
	if d.Allowed || d.Rule != "ai-mask" || !strings.Contains(d.Message, "cannot tie a response to one statement") {
		t.Fatalf("decision = %+v", d)
	}
}

// MaskedMediaType is the codec's classifier: for each type, the lane masker
// rewrites a length-delimited body exactly when MaskedMediaType says so.
func TestMaskedMediaTypeIsTheCodecsClassifier(t *testing.T) {
	for _, ct := range []string{"", "application/json", "application/problem+json", "text/json",
		"application/x-ndjson", "text/plain; charset=utf-8", "text/csv", "text/html", "application/xml",
		"application/yaml", "application/x-yaml", "application/javascript", "application/x-www-form-urlencoded",
		"application/octet-stream", "image/png", "application/pdf", "application/grpc", "multipart/form-data",
		"application/x-protobuf", "text/", "not a type", "application/json; =broken"} {
		g, err := gate.New(session.New(inspect.HTTP, session.Identity{Subject: "alice"}),
			gate.Config{Protocol: inspect.HTTP, Masker: aiMask})
		if err != nil {
			t.Fatal(err)
		}
		head := "HTTP/1.1 200 OK\r\nContent-Length: 15\r\n"
		if ct != "" {
			head += "Content-Type: " + ct + "\r\n"
		}
		out := respond(g, []byte(head+"\r\nada@example.com"))
		masked := !bytes.Contains(out, []byte("ada@example.com"))
		if masked != gate.MaskedMediaType(ct) {
			t.Errorf("%q: codec masked %t, MaskedMediaType %t", ct, masked, gate.MaskedMediaType(ct))
		}
	}
}

// Review cases: an opaque body, and an email across the 64 KiB text unit of
// a chunked body. Both go out unmasked through the codec, so a masked
// response of either kind is refused before any byte leaves.
func TestAMaskedResponseTheCodecPassesThroughIsRefused(t *testing.T) {
	chunked := splitTextResponse()
	for name, tc := range map[string]struct {
		resp []byte
		want string
	}{
		"octet-stream": {[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: 15\r\n\r\nada@example.com"),
			`media type "application/octet-stream"`},
		"invalid media type": {[]byte("HTTP/1.1 200 OK\r\nContent-Type: text/\r\nContent-Length: 15\r\n\r\nada@example.com"),
			"media type"},
		"chunked text across the unit": {chunked, `transfer encoding "chunked"`},
		"chunked json": {[]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n" +
			"1b\r\n{\"email\":\"ada@example.com\"}\r\n0\r\n\r\n"), `transfer encoding "chunked"`},
	} {
		t.Run(name, func(t *testing.T) {
			g := scopedGate(t, inspect.HTTP, gate.MaskScopeHTTP)
			g.Request(context.Background(), httpRequest("/masked"))
			d := g.Response(context.Background(), tc.resp)
			out := append(d.Payload, g.FlushResponse()...)
			if d.Allowed || len(out) != 0 || !strings.Contains(d.Message, tc.want) {
				t.Fatalf("allowed %t, %d bytes out, %q", d.Allowed, len(out), d.Message)
			}
		})
	}
	// An empty body of any type, and a 204, carry nothing to mask.
	for _, resp := range []string{
		"HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: 0\r\n\r\n",
		"HTTP/1.1 204 No Content\r\n\r\n",
	} {
		g := scopedGate(t, inspect.HTTP, gate.MaskScopeHTTP)
		g.Request(context.Background(), httpRequest("/masked"))
		if d := g.Response(context.Background(), []byte(resp)); !d.Allowed {
			t.Errorf("%q refused: %s", resp, d.Message)
		}
	}
}

// mysqlServerCodec is the real mysql codec on the server side, behind a
// client side that reports each read as one statement, so a test needs no
// handshake to reach the server stream.
type mysqlServerCodec struct {
	inspect.Codec
	rf gate.Reframer
}

func newMySQLServerCodec() inspect.Codec {
	insp, err := inspect.New(inspect.MySQL)
	if err != nil {
		panic(err)
	}
	c := insp.Codec()
	return &mysqlServerCodec{Codec: c, rf: c.(gate.Reframer)}
}

func (c *mysqlServerCodec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	if dir == inspect.FromClient {
		return []inspect.Statement{{Protocol: inspect.MySQL, Direction: dir, Text: string(data)}}, len(data), nil
	}
	return c.Codec.Decode(dir, data)
}
func (c *mysqlServerCodec) Rewrite(data []byte, mask func(string, []byte) []byte) ([]byte, inspect.ReframeResult, error) {
	return c.rf.Rewrite(data, mask)
}
func (c *mysqlServerCodec) Flush(mask func(string, []byte) []byte) []byte { return c.rf.Flush(mask) }

// Review case: the connection ends inside a masked response, and the codec
// holds a truncated frame whose complete first cell is sensitive. The
// codec's flush forwards that frame as it came, so the gate drops it.
func TestATruncatedMaskedFrameIsDroppedAtTheEnd(t *testing.T) {
	t.Run("postgres", func(t *testing.T) {
		g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
		ctx := context.Background()
		g.Request(ctx, pgQuery("SELECT mail, name FROM users -- masked"))
		desc := pgResult()[:30]
		// A two-cell DataRow cut inside its second cell.
		row := []byte("D\x00\x00\x00\x24\x00\x02\x00\x00\x00\x0fada@example.com\x00\x00\x00\x05Ad")
		out := g.Response(ctx, append(desc, row...)).Payload
		tail, err := g.FlushResponseChecked()
		if bytes.Contains(append(out, tail...), []byte("ada@example.com")) || !errors.Is(err, gate.ErrMaskedTailDropped) {
			t.Fatalf("out %q, tail %q, err %v", out, tail, err)
		}
	})
	t.Run("mysql", func(t *testing.T) {
		g, err := gate.New(session.New(inspect.MySQL, session.Identity{Subject: "alice"}), gate.Config{
			Protocol: inspect.MySQL, Policy: maskingPolicy{aiMask}, MaskScope: gate.MaskScopeSerial,
			CodecFactory: newMySQLServerCodec,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if d := g.Request(ctx, []byte("SELECT mail, name FROM users -- masked")); !d.Allowed {
			t.Fatalf("denied: %s", d.Message)
		}
		// A row packet of two length-encoded cells, cut inside the second.
		row := []byte("\x16\x00\x00\x05\x0fada@example.com\x05Ad")
		out := g.Response(ctx, row).Payload
		tail, ferr := g.FlushResponseChecked()
		if bytes.Contains(append(out, tail...), []byte("ada@example.com")) || !errors.Is(ferr, gate.ErrMaskedTailDropped) {
			t.Fatalf("out %q, tail %q, err %v", out, tail, ferr)
		}
	})
	t.Run("an unmasked exchange still flushes", func(t *testing.T) {
		g := scopedGate(t, inspect.Postgres, gate.MaskScopeSerial)
		ctx := context.Background()
		g.Request(ctx, pgQuery("SELECT mail FROM users"))
		row := []byte("D\x00\x00\x00\x24\x00\x02\x00\x00\x00\x0fada@example.com\x00\x00\x00\x05Ad")
		out := g.Response(ctx, append(pgResult()[:30], row...)).Payload
		tail, err := g.FlushResponseChecked()
		if err != nil || !bytes.Contains(append(out, tail...), []byte("ada@example.com")) {
			t.Errorf("out %q, tail %q, err %v", out, tail, err)
		}
	})
}

// splitTextResponse is a chunked text/plain body whose first chunk fills the
// codec's 64 KiB streaming unit with no newline, ending inside an email that
// the second chunk completes.
func splitTextResponse() []byte {
	unit := 64 << 10
	first := append(bytes.Repeat([]byte("a"), unit-5), "ada@e"...)
	second := []byte("xample.com\n")
	chunk := func(b []byte) string { return strconv.FormatInt(int64(len(b)), 16) + "\r\n" + string(b) + "\r\n" }
	return []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nTransfer-Encoding: chunked\r\n\r\n" +
		chunk(first) + chunk(second) + "0\r\n\r\n")
}

// Pre-existing codec limit, the reason chunked masked responses are refused:
// the lane masker itself lets an email split at the 64 KiB unit through.
func TestTheCodecSplitsAChunkedTextValueAtItsUnit(t *testing.T) {
	g, err := gate.New(session.New(inspect.HTTP, session.Identity{Subject: "alice"}),
		gate.Config{Protocol: inspect.HTTP, Masker: aiMask})
	if err != nil {
		t.Fatal(err)
	}
	out := respond(g, splitTextResponse())
	if bytes.Contains(out, []byte("[AI:EMAIL_ADDRESS]")) {
		t.Fatal("the codec now masks a value across its 64 KiB unit: admit chunked AI-masked " +
			"responses in maskableHead and delete this test")
	}
	if !bytes.Contains(out, []byte("ada@e")) || !bytes.Contains(out, []byte("xample.com")) {
		t.Fatalf("unexpected output shape")
	}
}
