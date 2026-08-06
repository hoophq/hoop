package postgres_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	_ "github.com/hoophq/hoopinspect/codec/postgres"
)

// query builds a 'Q' (simple query) message.
func query(sql string) []byte {
	var b bytes.Buffer
	b.WriteByte('Q')
	binary.Write(&b, binary.BigEndian, uint32(len(sql)+5))
	b.WriteString(sql)
	b.WriteByte(0)
	return b.Bytes()
}

// parse builds a 'P' (Parse) message.
func parse(name, sql string) []byte {
	var payload bytes.Buffer
	payload.WriteString(name)
	payload.WriteByte(0)
	payload.WriteString(sql)
	payload.WriteByte(0)
	binary.Write(&payload, binary.BigEndian, uint16(0)) // zero param types

	var b bytes.Buffer
	b.WriteByte('P')
	binary.Write(&b, binary.BigEndian, uint32(payload.Len()+4))
	b.Write(payload.Bytes())
	return b.Bytes()
}

func startup(user, db string) []byte {
	var params bytes.Buffer
	params.WriteString("user")
	params.WriteByte(0)
	params.WriteString(user)
	params.WriteByte(0)
	params.WriteString("database")
	params.WriteByte(0)
	params.WriteString(db)
	params.WriteByte(0)
	params.WriteByte(0)

	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(params.Len()+8))
	binary.Write(&b, binary.BigEndian, uint32(196608)) // protocol 3.0
	b.Write(params.Bytes())
	return b.Bytes()
}

func sslRequest() []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(8))
	binary.Write(&b, binary.BigEndian, uint32(80877103))
	return b.Bytes()
}

func newInspector(t *testing.T) *hoopinspect.Inspector {
	t.Helper()
	i, err := hoopinspect.New(hoopinspect.Postgres)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return i
}

func TestSimpleQuery(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		wantOp  hoopinspect.Operation
		wantTbl []string
	}{
		{"select", "SELECT name FROM customers", hoopinspect.OpSelect, []string{"customers"}},
		{"delete", "DELETE FROM customers WHERE id = 1", hoopinspect.OpDelete, []string{"customers"}},
		{"insert", "INSERT INTO orders (id) VALUES (1)", hoopinspect.OpInsert, []string{"orders"}},
		{"update", "UPDATE accounts SET x = 1", hoopinspect.OpUpdate, []string{"accounts"}},
		{"drop", "DROP TABLE customers", hoopinspect.OpDrop, []string{"customers"}},
		{"truncate", "TRUNCATE TABLE logs", hoopinspect.OpTruncate, []string{"logs"}},
		{"schema qualified", "SELECT * FROM public.customers", hoopinspect.OpSelect, []string{"public.customers"}},
		{"join", "SELECT * FROM a JOIN b ON a.id = b.id", hoopinspect.OpSelect, []string{"a", "b"}},
		{"drop if exists", "DROP TABLE IF EXISTS staging", hoopinspect.OpDrop, []string{"staging"}},
		{"delete only", "DELETE FROM ONLY parts", hoopinspect.OpDelete, []string{"parts"}},
		{"quoted ident", `SELECT * FROM "Customers"`, hoopinspect.OpSelect, []string{"customers"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			insp := newInspector(t)
			stmts, err := insp.Inspect(hoopinspect.FromClient, query(tc.sql))
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
			if !equalStrings(stmts[0].Tables, tc.wantTbl) {
				t.Errorf("Tables = %v, want %v", stmts[0].Tables, tc.wantTbl)
			}
		})
	}
}

// A multi-statement simple query must yield one Statement per statement.
// Classifying the whole payload by its leading verb would let a policy that
// denies DROP wave through "SELECT 1; DROP TABLE users".
func TestMultiStatementSplit(t *testing.T) {
	insp := newInspector(t)
	stmts, err := insp.Inspect(hoopinspect.FromClient,
		query("SELECT 1; DROP TABLE users; SELECT 2"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 3 {
		t.Fatalf("got %d statements, want 3: %+v", len(stmts), stmts)
	}
	if stmts[1].Operation != hoopinspect.OpDrop {
		t.Errorf("second statement Operation = %q, want drop", stmts[1].Operation)
	}
	if !equalStrings(stmts[1].Tables, []string{"users"}) {
		t.Errorf("second statement Tables = %v, want [users]", stmts[1].Tables)
	}
}

// A semicolon inside a literal is not a statement separator.
func TestSemicolonInLiteralIsNotSeparator(t *testing.T) {
	insp := newInspector(t)
	stmts, err := insp.Inspect(hoopinspect.FromClient,
		query("INSERT INTO notes (body) VALUES ('a;b;c')"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1: %+v", len(stmts), stmts)
	}
	if stmts[0].Operation != hoopinspect.OpInsert {
		t.Errorf("Operation = %q, want insert", stmts[0].Operation)
	}
}

func TestDollarQuotedBodyIsNotSplit(t *testing.T) {
	insp := newInspector(t)
	sql := `CREATE FUNCTION f() RETURNS int AS $$ BEGIN; RETURN 1; END; $$ LANGUAGE plpgsql`
	stmts, err := insp.Inspect(hoopinspect.FromClient, query(sql))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1: %+v", len(stmts), stmts)
	}
	if stmts[0].Operation != hoopinspect.OpCreate {
		t.Errorf("Operation = %q, want create", stmts[0].Operation)
	}
}

// A DELETE hidden in a string literal or a comment must NOT classify as a
// delete: the classifier strips both before looking for a verb. That is the
// concrete advantage of Operation over a deny-words match.
func TestLiteralsAndCommentsDoNotChangeOperation(t *testing.T) {
	for _, sql := range []string{
		`SELECT 'DROP TABLE customers' AS warning`,
		`/* DELETE FROM customers */ SELECT 1`,
		`SELECT 1 -- DROP TABLE customers`,
	} {
		insp := newInspector(t)
		stmts, err := insp.Inspect(hoopinspect.FromClient, query(sql))
		if err != nil {
			t.Fatalf("Inspect(%q): %v", sql, err)
		}
		if len(stmts) != 1 {
			t.Fatalf("%q: got %d statements, want 1", sql, len(stmts))
		}
		if stmts[0].Operation != hoopinspect.OpSelect {
			t.Errorf("%q: Operation = %q, want select", sql, stmts[0].Operation)
		}
	}
}

// A CTE must be classified by the verb it runs, not by WITH.
func TestCTEClassifiedByRealVerb(t *testing.T) {
	insp := newInspector(t)
	sql := `WITH doomed AS (SELECT id FROM customers) DELETE FROM orders WHERE id IN (SELECT id FROM doomed)`
	stmts, err := insp.Inspect(hoopinspect.FromClient, query(sql))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if stmts[0].Operation != hoopinspect.OpDelete {
		t.Errorf("Operation = %q, want delete", stmts[0].Operation)
	}
}

func TestParseMessage(t *testing.T) {
	insp := newInspector(t)
	stmts, err := insp.Inspect(hoopinspect.FromClient,
		parse("stmt1", "SELECT * FROM customers WHERE id = $1"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	if stmts[0].Metadata["pg.message"] != "Parse" {
		t.Errorf("pg.message = %q, want Parse", stmts[0].Metadata["pg.message"])
	}
	if stmts[0].Metadata["pg.statement"] != "stmt1" {
		t.Errorf("pg.statement = %q, want stmt1", stmts[0].Metadata["pg.statement"])
	}
}

func TestUnnamedParseHasNoStatementName(t *testing.T) {
	insp := newInspector(t)
	stmts, err := insp.Inspect(hoopinspect.FromClient, parse("", "SELECT 1"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if _, ok := stmts[0].Metadata["pg.statement"]; ok {
		t.Error("unnamed Parse should not set pg.statement")
	}
}

// Handshake packets have no type tag; mistaking one for a message would
// desynchronize the whole stream.
func TestHandshakeIsSkipped(t *testing.T) {
	insp := newInspector(t)
	stream := append(sslRequest(), startup("alice", "appdb")...)
	stream = append(stream, query("SELECT 1")...)

	stmts, err := insp.Inspect(hoopinspect.FromClient, stream)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1: %+v", len(stmts), stmts)
	}
	if stmts[0].Text != "SELECT 1" {
		t.Errorf("Text = %q, want SELECT 1", stmts[0].Text)
	}
	if insp.Buffered() != 0 {
		t.Errorf("Buffered = %d, want 0", insp.Buffered())
	}
}

// A statement split across TCP segments must be reassembled, not classified
// from a fragment.
func TestSplitAcrossReads(t *testing.T) {
	full := query("DELETE FROM customers WHERE id = 1")

	for split := 1; split < len(full); split++ {
		insp := newInspector(t)

		got, err := insp.Inspect(hoopinspect.FromClient, full[:split])
		if err != nil {
			t.Fatalf("split=%d first Inspect: %v", split, err)
		}
		if len(got) != 0 {
			t.Fatalf("split=%d: statement emitted from a partial message", split)
		}

		got, err = insp.Inspect(hoopinspect.FromClient, full[split:])
		if err != nil {
			t.Fatalf("split=%d second Inspect: %v", split, err)
		}
		if len(got) != 1 {
			t.Fatalf("split=%d: got %d statements, want 1", split, len(got))
		}
		if got[0].Operation != hoopinspect.OpDelete {
			t.Errorf("split=%d: Operation = %q, want delete", split, got[0].Operation)
		}
		if insp.Buffered() != 0 {
			t.Errorf("split=%d: Buffered = %d, want 0", split, insp.Buffered())
		}
	}
}

// The caller may reuse its read buffer the moment Inspect returns, so retained
// bytes must be copied, not aliased.
func TestRetainedBytesAreCopied(t *testing.T) {
	insp := newInspector(t)
	full := query("SELECT name FROM customers")

	scratch := make([]byte, len(full))
	copy(scratch, full[:10])
	if _, err := insp.Inspect(hoopinspect.FromClient, scratch[:10]); err != nil {
		t.Fatalf("first Inspect: %v", err)
	}

	// Scribble over the caller's buffer, as a real reader would.
	for i := range scratch {
		scratch[i] = 0xFF
	}

	copy(scratch, full[10:])
	stmts, err := insp.Inspect(hoopinspect.FromClient, scratch[:len(full)-10])
	if err != nil {
		t.Fatalf("second Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	if stmts[0].Text != "SELECT name FROM customers" {
		t.Errorf("Text = %q: retained bytes were aliased, not copied", stmts[0].Text)
	}
}

func TestServerDirectionYieldsNothing(t *testing.T) {
	insp := newInspector(t)
	stmts, err := insp.Inspect(hoopinspect.FromServer, query("SELECT 1"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 0 {
		t.Errorf("got %d statements from server direction, want 0", len(stmts))
	}
	if insp.Buffered() != 0 {
		t.Errorf("server bytes should be consumed, Buffered = %d", insp.Buffered())
	}
}

// A length field below the 4-byte minimum cannot be resynchronized.
func TestMalformedLengthErrors(t *testing.T) {
	insp := newInspector(t)
	bad := []byte{'Q', 0, 0, 0, 1, 'x'}
	if _, err := insp.Inspect(hoopinspect.FromClient, bad); err == nil {
		t.Fatal("expected an error for a length below the minimum")
	}
}

func TestMultipleMessagesInOneRead(t *testing.T) {
	insp := newInspector(t)
	stream := append(query("SELECT 1"), parse("p", "DELETE FROM t")...)
	stream = append(stream, query("SELECT 2")...)

	stmts, err := insp.Inspect(hoopinspect.FromClient, stream)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 3 {
		t.Fatalf("got %d statements, want 3", len(stmts))
	}
	if stmts[1].Operation != hoopinspect.OpDelete {
		t.Errorf("stmts[1].Operation = %q, want delete", stmts[1].Operation)
	}
}

func TestStatementStringElidesLongText(t *testing.T) {
	s := hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Direction: hoopinspect.FromClient,
		Operation: hoopinspect.OpInsert,
		Text:      strings.Repeat("x", 500),
	}
	if got := s.String(); len(got) > 200 {
		t.Errorf("String() length = %d, want elided under 200", len(got))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
