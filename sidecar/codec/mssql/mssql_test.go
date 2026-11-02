// The MSSQL routing-redirect refusal, driven through a real Gate.
//
// A routing ENVCHANGE tells the client to reconnect somewhere else. Honored,
// it moves the session to a socket the relay does not hold, and the audit
// trail simply stops with no sign anything was wrong. The codec refuses it
// with ErrStreamUnsafe; these two cases assert the refusal survives the trip
// through gate.New and reaches a decision, and that the guard retires on both
// direction codecs once login completes.
//
// The wire-level cases for the same guard live with the codec, in
// libhoop/v2/codec/mssql. Only the ones needing a Gate are here.

package mssql_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

func ucs2(s string) []byte {
	var b bytes.Buffer
	for _, r := range s {
		binary.Write(&b, binary.LittleEndian, uint16(r))
	}
	return b.Bytes()
}

// tdsPacket wraps a body in the 8-byte TDS header.
func tdsPacket(typ byte, eom bool, body []byte) []byte {
	var b bytes.Buffer
	b.WriteByte(typ)
	status := byte(0x00)
	if eom {
		status = 0x01
	}
	b.WriteByte(status)
	binary.Write(&b, binary.BigEndian, uint16(8+len(body)))
	b.Write([]byte{0, 0}) // SPID
	b.WriteByte(1)        // packet id
	b.WriteByte(0)        // window
	b.Write(body)
	return b.Bytes()
}

// sqlBatchBody builds a SQLBatch body: ALL_HEADERS then the UCS-2 statement.
func sqlBatchBody(sql string) []byte {
	var b bytes.Buffer
	const allHeadersLen = 22
	binary.Write(&b, binary.LittleEndian, uint32(allHeadersLen))
	b.Write(make([]byte, allHeadersLen-4))
	b.Write(ucs2(sql))
	return b.Bytes()
}

func sqlBatch(sql string) []byte {
	return tdsPacket(0x01, true, sqlBatchBody(sql))
}

// rpcExecuteSQL builds an RPCRequest calling sp_executesql with the statement
// as the first NVARCHAR parameter.
func routingEnvChange(server string, port uint16) []byte {
	name := ucs2(server)

	// RoutingData: Protocol(1) Port(2) AltServerLen(2) AltServer
	var routing bytes.Buffer
	routing.WriteByte(0) // TCP
	binary.Write(&routing, binary.LittleEndian, port)
	binary.Write(&routing, binary.LittleEndian, uint16(len(name)/2))
	routing.Write(name)

	// EnvValueData: Type(1) ValueLength(2) RoutingData OldValueLength(2)
	var value bytes.Buffer
	value.WriteByte(20) // routing
	binary.Write(&value, binary.LittleEndian, uint16(routing.Len()))
	value.Write(routing.Bytes())
	binary.Write(&value, binary.LittleEndian, uint16(0)) // OldValue

	var tok bytes.Buffer
	tok.WriteByte(0xe3)
	binary.Write(&tok, binary.LittleEndian, uint16(value.Len()))
	tok.Write(value.Bytes())
	return tok.Bytes()
}

// A Kerberos login is opaque to this codec by design: the AP-REQ lives in
// packet type 0x11, which carries no SQL and must reach the server byte for
// byte. If the decoder ever reported a statement here, a policy would be
// evaluating a service ticket as if it were text.
func loginAck() []byte {
	name := ucs2("Microsoft SQL Server")
	var b bytes.Buffer
	b.WriteByte(0xad)
	binary.Write(&b, binary.LittleEndian, uint16(10+len(name)))
	b.WriteByte(1)                          // Interface
	b.Write([]byte{0x04, 0x00, 0x00, 0x74}) // TDSVersion 7.4
	b.WriteByte(byte(len(name) / 2))        // ProgName length, in characters
	b.Write(name)
	b.Write([]byte{16, 0, 0x07, 0xd0}) // major, minor, build hi/lo
	return b.Bytes()
}

// loginResponse builds the server's reply to LOGIN7: LOGINACK, whatever the
// caller wants in between, and the terminating DONE.
func loginResponse(between ...[]byte) []byte {
	var body bytes.Buffer
	body.Write(loginAck())
	for _, b := range between {
		body.Write(b)
	}
	body.Write(doneToken())
	return tdsPacket(0x04, true, body.Bytes())
}

// Result-set bytes are attacker-influenced: a user can SELECT a string that
// happens to contain 0xE3. Scanning those would let any user with query
// access kill their own connection, so the guard retires once the login
// response has been seen — which is also the only place MS-TDS puts routing
// information.
//
// Retirement keys off a SERVER-side observation. The codec is Duplex now
// (one instance drives both directions, for prepared handles), so the old
// failure — a flag set on the client-side instance that the server-side one
// never saw — cannot recur, and this test keeps proving the retirement
// happens at all.
func mssqlGate(t *testing.T) *gate.Gate {
	t.Helper()
	g, err := gate.New(
		session.New(inspect.MSSQL, session.Identity{Subject: "alice@example.com"}),
		gate.Config{Protocol: inspect.MSSQL},
	)
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	return g
}

// The regression test for the bug this guard shipped with: the guard once
// retired on a flag set while decoding a CLIENT query, on a codec instance
// the server side never shared, so the scan never retired and result rows
// were searched for redirect tokens forever. It goes through gate.New so it
// sees the codec as the relay builds it.
func TestRoutingGuardRetiresAcrossDirectionCodecs(t *testing.T) {
	g := mssqlGate(t)
	ctx := context.Background()

	if d := g.Response(ctx, loginResponse()); !d.Allowed {
		t.Fatalf("clean login response denied: %s", d.Message)
	}
	if d := g.Request(ctx, sqlBatch("SELECT payload FROM blobs")); !d.Allowed {
		t.Fatalf("query denied: %s", d.Message)
	}

	// Row data whose bytes spell a routing ENVCHANGE. A user can put these
	// in a table and SELECT them.
	rows := tdsPacket(0x04, true, routingEnvChange("secondary.corp.example", 1433))
	if d := g.Response(ctx, rows); !d.Allowed {
		t.Fatalf("post-login result data was refused as a redirect: %s", d.Message)
	}
}

// And the guard still fires where it has to, down the same path.
func TestRoutingRedirectIsDeniedThroughTheGate(t *testing.T) {
	g := mssqlGate(t)

	redirect := loginResponse(routingEnvChange("secondary.corp.example", 1433))
	d := g.Response(context.Background(), redirect)

	if d.Allowed {
		t.Fatal("a routing redirect was forwarded to the client")
	}
	if d.Rule != "stream-unsafe" {
		t.Errorf("rule = %q, want stream-unsafe", d.Rule)
	}
	if !strings.Contains(d.Message, "secondary.corp.example:1433") {
		t.Errorf("message does not name the redirect target: %s", d.Message)
	}
}

// A stray 0xE3 with no valid structure behind it must not trip the guard.

// doneToken closes a server response batch.
func doneToken() []byte {
	b := []byte{0xfd}
	b = binary.LittleEndian.AppendUint16(b, 0)
	b = binary.LittleEndian.AppendUint16(b, 0)
	return binary.LittleEndian.AppendUint64(b, 0)
}

// rpcByName builds an RPCRequest calling proc by name, the way FreeTDS's
// callproc does, with each value as an NVARCHAR parameter.
func rpcByName(proc string, args ...string) []byte {
	var body bytes.Buffer
	const allHeadersLen = 22
	binary.Write(&body, binary.LittleEndian, uint32(allHeadersLen))
	body.Write(make([]byte, allHeadersLen-4))
	binary.Write(&body, binary.LittleEndian, uint16(len([]rune(proc))))
	body.Write(ucs2(proc))
	binary.Write(&body, binary.LittleEndian, uint16(0)) // OptionFlags
	for _, a := range args {
		body.WriteByte(0) // unnamed
		body.WriteByte(0) // StatusFlags
		body.WriteByte(0xe7)
		binary.Write(&body, binary.LittleEndian, uint16(8000))
		body.Write(make([]byte, 5))
		v := ucs2(a)
		binary.Write(&body, binary.LittleEndian, uint16(len(v)))
		body.Write(v)
	}
	return tdsPacket(0x03, true, body.Bytes())
}

// auditSink keeps every statement event.
type auditSink struct{ events []audit.Event }

// Write keeps the row a denial produces (a violation) and the row an
// allowed statement produces (a statement); the lifecycle rows are noise here.
func (s *auditSink) Write(_ context.Context, ev audit.Event) error {
	if ev.Kind == audit.KindStatement || ev.Kind == audit.KindViolation {
		s.events = append(s.events, ev)
	}
	return nil
}
func (s *auditSink) Close() error { return nil }

// A procedure call by name used to cross the relay with no statement, so a
// rule that refused `SELECT * FROM dbo.secrets` as a batch let
// callproc("sp_executesql", ("SELECT * FROM dbo.secrets",)) through, and
// callproc("dbo.get_secrets") left no audit record. Every call is a
// statement now: the same rule refuses the same SQL whichever message type
// carries it, a bare EXEC is unreadable to the lexer and refused by a rule
// that demands a table match, and the trail records the call either way.
func TestProcedureCallsReachThePolicy(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "no-secrets", Type: policy.MatchTable, Tables: []string{"secrets"},
		RequireTableMatch: true, Message: "secrets is off limits",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	ctx := context.Background()

	cases := []struct {
		name     string
		pkt      []byte
		wantText string
		wantOp   inspect.Operation
	}{
		{"batch", sqlBatch("SELECT * FROM dbo.secrets"), "SELECT * FROM dbo.secrets", inspect.OpSelect},
		{"callproc-sp_executesql", rpcByName("sp_executesql", "SELECT * FROM dbo.secrets"), "SELECT * FROM dbo.secrets", inspect.OpSelect},
		{"callproc-by-name", rpcByName("dbo.get_secrets"), "EXEC dbo.get_secrets", inspect.OpUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &auditSink{}
			g, err := gate.New(
				session.New(inspect.MSSQL, session.Identity{Subject: "alice@example.com"}),
				gate.Config{Protocol: inspect.MSSQL, Policy: rules, Audit: sink},
			)
			if err != nil {
				t.Fatalf("gate.New: %v", err)
			}
			d := g.Request(ctx, tc.pkt)
			if d.Allowed {
				t.Fatalf("forwarded: %+v", d.Statements)
			}
			if d.Rule != "no-secrets" {
				t.Errorf("rule = %q, want no-secrets", d.Rule)
			}
			if len(sink.events) != 1 || sink.events[0].Kind != audit.KindViolation {
				t.Fatalf("got %+v, want one violation", sink.events)
			}
			ev := sink.events[0]
			if ev.Statement != tc.wantText || ev.Operation != tc.wantOp || ev.Allowed {
				t.Errorf("event = {Statement:%q Operation:%q Allowed:%v}, want {%q %q false}",
					ev.Statement, ev.Operation, ev.Allowed, tc.wantText, tc.wantOp)
			}
			if ev.Metadata["mssql.proc"] == "" && tc.name != "batch" {
				t.Error("mssql.proc missing from the audit record of a procedure call")
			}
		})
	}
}

// With no rule standing in its way a call is forwarded, and it is still in
// the trail: the complaint was the silence, not only the bypass.
func TestAllowedProcedureCallIsAudited(t *testing.T) {
	sink := &auditSink{}
	g, err := gate.New(
		session.New(inspect.MSSQL, session.Identity{Subject: "alice@example.com"}),
		gate.Config{Protocol: inspect.MSSQL, Audit: sink},
	)
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	d := g.Request(context.Background(), rpcByName("dbo.delete_customer", "3"))
	if !d.Allowed {
		t.Fatalf("refused with no policy: %s", d.Message)
	}
	if len(sink.events) != 1 || sink.events[0].Statement != "EXEC dbo.delete_customer" {
		t.Fatalf("events = %+v, want one for EXEC dbo.delete_customer", sink.events)
	}
	if strings.Contains(sink.events[0].Statement, "3") {
		t.Error("a parameter value reached the audit statement")
	}
}
