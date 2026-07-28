package mssql_test

import (
	"bytes"
	"encoding/binary"
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
