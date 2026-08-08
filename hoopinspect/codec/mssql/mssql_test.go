package mssql_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/codec/mssql"
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
func rpcExecuteSQL(sql string) []byte {
	var body bytes.Buffer
	const allHeadersLen = 22
	binary.Write(&body, binary.LittleEndian, uint32(allHeadersLen))
	body.Write(make([]byte, allHeadersLen-4))

	body.Write([]byte{0xff, 0xff})                         // well-known proc
	binary.Write(&body, binary.LittleEndian, uint16(10))   // sp_executesql
	binary.Write(&body, binary.LittleEndian, uint16(0))    // option flags
	body.WriteByte(0)                                      // param name length
	body.WriteByte(0)                                      // status flags
	body.WriteByte(0xe7)                                   // NVARCHARTYPE
	binary.Write(&body, binary.LittleEndian, uint16(8000)) // max length
	body.Write(make([]byte, 5))                            // collation

	payload := ucs2(sql)
	binary.Write(&body, binary.LittleEndian, uint16(len(payload)))
	body.Write(payload)

	return tdsPacket(0x03, true, body.Bytes())
}

func newInspector(t *testing.T) *hoopinspect.Inspector {
	t.Helper()
	i, err := hoopinspect.New(hoopinspect.MSSQL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return i
}

func TestSQLBatch(t *testing.T) {
	tests := []struct {
		sql    string
		wantOp hoopinspect.Operation
		wantTb string
	}{
		{"SELECT name FROM customers", hoopinspect.OpSelect, "customers"},
		{"DELETE FROM customers WHERE id = 1", hoopinspect.OpDelete, "customers"},
		{"UPDATE accounts SET balance = 0", hoopinspect.OpUpdate, "accounts"},
		{"DROP TABLE staging", hoopinspect.OpDrop, "staging"},
	}
	for _, tc := range tests {
		t.Run(tc.sql, func(t *testing.T) {
			insp := newInspector(t)
			stmts, err := insp.Inspect(hoopinspect.FromClient, sqlBatch(tc.sql))
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if len(stmts) != 1 {
				t.Fatalf("got %d statements, want 1: %+v", len(stmts), stmts)
			}
			if stmts[0].Text != tc.sql {
				t.Errorf("Text = %q, want %q", stmts[0].Text, tc.sql)
			}
			if stmts[0].Operation != tc.wantOp {
				t.Errorf("Operation = %q, want %q", stmts[0].Operation, tc.wantOp)
			}
			if len(stmts[0].Tables) != 1 || stmts[0].Tables[0] != tc.wantTb {
				t.Errorf("Tables = %v, want [%s]", stmts[0].Tables, tc.wantTb)
			}
			if stmts[0].Metadata["mssql.message"] != "SQLBatch" {
				t.Errorf("mssql.message = %q", stmts[0].Metadata["mssql.message"])
			}
		})
	}
}

// Parameterized queries from .NET/JDBC arrive as sp_executesql RPC calls, not
// as SQLBatch. A decoder that only handled SQLBatch would miss most real
// application traffic.
func TestRPCExecuteSQL(t *testing.T) {
	insp := newInspector(t)
	sql := "DELETE FROM customers WHERE id = @p1"

	stmts, err := insp.Inspect(hoopinspect.FromClient, rpcExecuteSQL(sql))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1: %+v", len(stmts), stmts)
	}
	if stmts[0].Text != sql {
		t.Errorf("Text = %q, want %q", stmts[0].Text, sql)
	}
	if stmts[0].Operation != hoopinspect.OpDelete {
		t.Errorf("Operation = %q, want delete", stmts[0].Operation)
	}
	if stmts[0].Metadata["mssql.proc"] != "sp_executesql" {
		t.Errorf("mssql.proc = %q, want sp_executesql", stmts[0].Metadata["mssql.proc"])
	}
}

// A message spanning multiple packets must be reassembled before parsing.
// Classifying the first fragment alone would see "DELETE FROM cust" and could
// still be wrong about the table.
func TestMultiPacketReassembly(t *testing.T) {
	sql := "DELETE FROM customers WHERE id = 1"
	body := sqlBatchBody(sql)

	mid := len(body) / 2
	if mid%2 != 0 {
		mid++ // keep the UCS-2 split on a character boundary
	}
	first := tdsPacket(0x01, false, body[:mid])
	second := tdsPacket(0x01, true, body[mid:])

	insp := newInspector(t)

	stmts, err := insp.Inspect(hoopinspect.FromClient, first)
	if err != nil {
		t.Fatalf("first Inspect: %v", err)
	}
	if len(stmts) != 0 {
		t.Fatalf("statement emitted before EOM: %+v", stmts)
	}

	stmts, err = insp.Inspect(hoopinspect.FromClient, second)
	if err != nil {
		t.Fatalf("second Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements after EOM, want 1", len(stmts))
	}
	if stmts[0].Text != sql {
		t.Errorf("Text = %q, want %q", stmts[0].Text, sql)
	}
}

func TestSplitAcrossReads(t *testing.T) {
	full := sqlBatch("SELECT name FROM customers")

	for split := 1; split < len(full); split++ {
		insp := newInspector(t)

		if got, err := insp.Inspect(hoopinspect.FromClient, full[:split]); err != nil {
			t.Fatalf("split=%d first: %v", split, err)
		} else if len(got) != 0 {
			t.Fatalf("split=%d: emitted from a partial packet", split)
		}

		got, err := insp.Inspect(hoopinspect.FromClient, full[split:])
		if err != nil {
			t.Fatalf("split=%d second: %v", split, err)
		}
		if len(got) != 1 {
			t.Fatalf("split=%d: got %d statements, want 1", split, len(got))
		}
	}
}

// Each Inspector must get its own codec: MSSQL reassembles across packets, so
// a shared instance would let one connection's fragment leak into another's
// statement.
func TestInspectorsDoNotShareState(t *testing.T) {
	a := newInspector(t)
	b := newInspector(t)

	bodyA := sqlBatchBody("DELETE FROM secrets")
	if _, err := a.Inspect(hoopinspect.FromClient, tdsPacket(0x01, false, bodyA)); err != nil {
		t.Fatalf("a first: %v", err)
	}

	stmts, err := b.Inspect(hoopinspect.FromClient, sqlBatch("SELECT 1"))
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("b got %d statements, want 1", len(stmts))
	}
	if stmts[0].Text != "SELECT 1" {
		t.Errorf("b saw %q — codec state leaked between inspectors", stmts[0].Text)
	}
}

func TestNonSQLPacketTypesIgnored(t *testing.T) {
	insp := newInspector(t)
	prelogin := tdsPacket(0x12, true, []byte{0xff})
	stmts, err := insp.Inspect(hoopinspect.FromClient, prelogin)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 0 {
		t.Errorf("got %d statements from PRELOGIN, want 0", len(stmts))
	}
}

func TestMalformedLengthErrors(t *testing.T) {
	insp := newInspector(t)
	bad := []byte{0x01, 0x01, 0x00, 0x02, 0, 0, 1, 0}
	if _, err := insp.Inspect(hoopinspect.FromClient, bad); err == nil {
		t.Fatal("expected an error for a length below the header size")
	}
}

// DetectSSPI is the guard against silently attempting Kerberos interposition.
// The SSPI blob is a service ticket bound to the server's SPN: it cannot be
// minted or rewritten, only relayed, and channel binding defeats even that.
func TestDetectSSPI(t *testing.T) {
	build := func(withSSPI bool) []byte {
		const fixedLen = 94
		body := make([]byte, fixedLen)

		user := ucs2("appuser")
		userOff := fixedLen
		body = append(body, user...)
		binary.LittleEndian.PutUint16(body[40:], uint16(userOff))
		binary.LittleEndian.PutUint16(body[42:], uint16(len(user)/2))

		if withSSPI {
			sspiOff := len(body)
			blob := []byte{0x4e, 0x54, 0x4c, 0x4d, 0x53, 0x53, 0x50, 0x00}
			body = append(body, blob...)
			binary.LittleEndian.PutUint16(body[78:], uint16(sspiOff))
			binary.LittleEndian.PutUint16(body[80:], uint16(len(blob)))
		}
		return tdsPacket(0x10, true, body)
	}

	t.Run("password auth", func(t *testing.T) {
		info, ok := mssql.DetectSSPI(build(false))
		if !ok {
			t.Fatal("DetectSSPI returned ok=false for a valid LOGIN7")
		}
		if info.UsesSSPI {
			t.Error("UsesSSPI = true for a password login")
		}
		if info.Username != "appuser" {
			t.Errorf("Username = %q, want appuser", info.Username)
		}
	})

	t.Run("integrated auth", func(t *testing.T) {
		info, ok := mssql.DetectSSPI(build(true))
		if !ok {
			t.Fatal("DetectSSPI returned ok=false for a valid LOGIN7")
		}
		if !info.UsesSSPI {
			t.Error("UsesSSPI = false for a login carrying an SSPI blob — " +
				"a proxy would silently attempt to rewrite Kerberos credentials")
		}
	})

	t.Run("not a login packet", func(t *testing.T) {
		if _, ok := mssql.DetectSSPI(sqlBatch("SELECT 1")); ok {
			t.Error("DetectSSPI accepted a SQLBatch as LOGIN7")
		}
	})
}

func TestIsPrelogin(t *testing.T) {
	if !mssql.IsPrelogin(tdsPacket(0x12, true, []byte{0x00})) {
		t.Error("IsPrelogin = false for a PRELOGIN packet")
	}
	if mssql.IsPrelogin(sqlBatch("SELECT 1")) {
		t.Error("IsPrelogin = true for a SQLBatch")
	}
}

func TestServerDirectionYieldsNothing(t *testing.T) {
	insp := newInspector(t)
	stmts, err := insp.Inspect(hoopinspect.FromServer, sqlBatch("SELECT 1"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 0 {
		t.Errorf("got %d statements from server direction, want 0", len(stmts))
	}
}

// --- Integrated authentication passthrough -------------------------------

// routingEnvChange builds a login-response body carrying a routing ENVCHANGE
// pointing at server:port, which is how SQL Server tells a client to go
// reconnect somewhere else (AlwaysOn read-only routing, Azure gateway).
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
func TestSSPIPacketsYieldNoStatementsAndAreFullyConsumed(t *testing.T) {
	c := &mssql.Codec{}

	// A realistic integrated login: PRELOGIN, LOGIN7, then two SSPI rounds.
	var stream bytes.Buffer
	stream.Write(tdsPacket(0x12, true, []byte{0x00, 0x00, 0x1a, 0x00, 0x06, 0xff}))
	stream.Write(tdsPacket(0x10, true, make([]byte, 94)))
	stream.Write(tdsPacket(0x11, true, []byte{0x60, 0x82, 0x01, 0x0b, 0x06, 0x09, 0x2a}))
	stream.Write(tdsPacket(0x11, true, []byte{0xa1, 0x81, 0xd4, 0x30, 0x81, 0xd1}))

	stmts, n, err := c.Decode(hoopinspect.FromClient, stream.Bytes())
	if err != nil {
		t.Fatalf("login stream errored: %v", err)
	}
	if len(stmts) != 0 {
		t.Errorf("login produced %d statement(s); the SSPI blob is not SQL", len(stmts))
	}
	if n != stream.Len() {
		t.Errorf("consumed %d of %d bytes; unconsumed login bytes would be replayed", n, stream.Len())
	}

	// And the codec is still usable for the statement that follows.
	stmts, _, err = c.Decode(hoopinspect.FromClient, sqlBatch("SELECT 1"))
	if err != nil {
		t.Fatalf("post-login decode failed: %v", err)
	}
	if len(stmts) != 1 || stmts[0].Text != "SELECT 1" {
		t.Errorf("statement after an SSPI login = %+v, want SELECT 1", stmts)
	}
}

func TestIsSSPIMessage(t *testing.T) {
	if !mssql.IsSSPIMessage(tdsPacket(0x11, true, []byte{1, 2, 3})) {
		t.Error("a 0x11 packet was not recognized as an SSPI continuation")
	}
	if mssql.IsSSPIMessage(sqlBatch("SELECT 1")) {
		t.Error("a SQLBatch was misreported as an SSPI continuation")
	}
	if mssql.IsSSPIMessage([]byte{0x11}) {
		t.Error("a truncated header was accepted")
	}
}

// --- The routing-redirect bypass guard -----------------------------------

// This is the guard that matters most. A routing ENVCHANGE tells the driver
// to reconnect elsewhere, and every driver obeys silently. Forwarded, the
// client lands on a socket the relay does not hold: no policy, no audit, and
// nothing in the trail saying the session went unwatched.
func TestRoutingRedirectIsRefused(t *testing.T) {
	c := &mssql.Codec{}

	reply := tdsPacket(0x04, true, routingEnvChange("secondary.corp.example", 1433))
	_, _, err := c.Decode(hoopinspect.FromServer, reply)

	if !errors.Is(err, hoopinspect.ErrStreamUnsafe) {
		t.Fatalf("routing redirect returned %v, want ErrStreamUnsafe", err)
	}
	// The operator has to be able to tell WHERE it was being sent, or the
	// refusal is unactionable.
	if !strings.Contains(err.Error(), "secondary.corp.example") {
		t.Errorf("error does not name the redirect target: %v", err)
	}
	if !strings.Contains(err.Error(), "1433") {
		t.Errorf("error does not name the redirect port: %v", err)
	}
}

// A redirect that arrives split across two reads must still be caught. The
// relay reads whatever the kernel gives it, so a seam mid-token is ordinary,
// and a guard that only worked on aligned reads would fail open at random.
func TestRoutingRedirectSplitAcrossReadsIsStillRefused(t *testing.T) {
	c := &mssql.Codec{}

	reply := tdsPacket(0x04, true, routingEnvChange("secondary.corp.example", 1433))
	cut := len(reply) / 2

	if _, n, err := c.Decode(hoopinspect.FromServer, reply[:cut]); err != nil {
		t.Fatalf("first half errored: %v", err)
	} else if n != 0 {
		t.Errorf("consumed %d bytes of a partial packet; the tail would be lost", n)
	}

	if _, _, err := c.Decode(hoopinspect.FromServer, reply); !errors.Is(err, hoopinspect.ErrStreamUnsafe) {
		t.Fatalf("reassembled redirect returned %v, want ErrStreamUnsafe", err)
	}
}

// An ordinary login response must pass. A guard that refused every session
// would "pass" the test above and be useless.
func TestOrdinaryLoginResponseIsAllowed(t *testing.T) {
	c := &mssql.Codec{}

	// ENVCHANGE type 1 (database change) plus a LOGINACK-ish token. Both
	// carry the same 0xE3 token byte the scan looks for; neither is routing.
	var body bytes.Buffer
	body.Write([]byte{0xe3, 0x08, 0x00, 0x01, 0x02, 'd', 0x00, 0x02, 'm', 0x00})
	body.Write([]byte{0xad, 0x06, 0x00, 0x01, 0x74, 0x00, 0x00, 0x04})
	body.Write([]byte{0xfd, 0x00, 0x00, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0})

	if _, _, err := c.Decode(hoopinspect.FromServer, tdsPacket(0x04, true, body.Bytes())); err != nil {
		t.Fatalf("an ordinary login response was refused: %v", err)
	}
}

// Result-set bytes are attacker-influenced: a user can SELECT a string that
// happens to contain 0xE3. Scanning those would let any user with query
// access kill their own connection, so the guard retires once the login is
// over — which is also the only place MS-TDS puts routing information.
func TestRoutingScanRetiresAfterLogin(t *testing.T) {
	c := &mssql.Codec{}

	if _, _, err := c.Decode(hoopinspect.FromClient, sqlBatch("SELECT payload FROM blobs")); err != nil {
		t.Fatalf("query decode failed: %v", err)
	}

	// The exact bytes of a routing redirect, now appearing as row data.
	rows := tdsPacket(0x04, true, routingEnvChange("secondary.corp.example", 1433))
	if _, n, err := c.Decode(hoopinspect.FromServer, rows); err != nil {
		t.Fatalf("post-login response was refused: %v", err)
	} else if n != len(rows) {
		t.Errorf("consumed %d of %d response bytes", n, len(rows))
	}
}

// A stray 0xE3 with no valid structure behind it must not trip the guard.
func TestNonRoutingBytesDoNotTripTheGuard(t *testing.T) {
	c := &mssql.Codec{}

	body := bytes.Repeat([]byte{0xe3, 0x14, 0x00, 0x14, 0xff, 0xff}, 40)
	if _, _, err := c.Decode(hoopinspect.FromServer, tdsPacket(0x04, true, body)); err != nil {
		t.Fatalf("structurally invalid 0xE3 bytes were treated as a redirect: %v", err)
	}
}
