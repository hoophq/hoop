package mysql_test

import (
	"bytes"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/codec/mysql"
)

// packet wraps a payload in the 4-byte MySQL header.
func packet(seq byte, payload []byte) []byte {
	var b bytes.Buffer
	n := len(payload)
	b.WriteByte(byte(n))
	b.WriteByte(byte(n >> 8))
	b.WriteByte(byte(n >> 16))
	b.WriteByte(seq)
	b.Write(payload)
	return b.Bytes()
}

func comQuery(sql string) []byte {
	return packet(0, append([]byte{0x03}, sql...))
}

func comStmtPrepare(sql string) []byte {
	return packet(0, append([]byte{0x16}, sql...))
}

// handshakeResponse is what the client sends at seq 1 in reply to the server
// greeting. Its first byte is a capability flag, NOT a command byte.
func handshakeResponse() []byte {
	return packet(1, []byte{0x03, 0xa2, 0x3a, 0x00, 'r', 'o', 'o', 't', 0})
}

func newInspector(t *testing.T) *hoopinspect.Inspector {
	t.Helper()
	i, err := hoopinspect.New(hoopinspect.MySQL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Tests drive commands directly; skip the handshake phase.
	return i
}

func TestComQuery(t *testing.T) {
	tests := []struct {
		sql    string
		wantOp hoopinspect.Operation
		wantTb string
	}{
		{"SELECT name FROM customers", hoopinspect.OpSelect, "customers"},
		{"DELETE FROM customers WHERE id = 1", hoopinspect.OpDelete, "customers"},
		{"INSERT INTO orders VALUES (1)", hoopinspect.OpInsert, "orders"},
		{"DROP TABLE staging", hoopinspect.OpDrop, "staging"},
		{"TRUNCATE TABLE logs", hoopinspect.OpTruncate, "logs"},
	}
	for _, tc := range tests {
		t.Run(tc.sql, func(t *testing.T) {
			insp := newInspector(t)
			stmts, err := insp.Inspect(hoopinspect.FromClient, comQuery(tc.sql))
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
			if stmts[0].Metadata["mysql.command"] != "COM_QUERY" {
				t.Errorf("mysql.command = %q", stmts[0].Metadata["mysql.command"])
			}
		})
	}
}

// MySQL backtick-quoted identifiers must survive into the table list.
func TestBacktickIdentifiers(t *testing.T) {
	insp := newInspector(t)
	stmts, err := insp.Inspect(hoopinspect.FromClient,
		comQuery("SELECT * FROM `Customers` WHERE id = 1"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts[0].Tables) != 1 || stmts[0].Tables[0] != "customers" {
		t.Errorf("Tables = %v, want [customers]", stmts[0].Tables)
	}
}

func TestComStmtPrepare(t *testing.T) {
	insp := newInspector(t)
	stmts, err := insp.Inspect(hoopinspect.FromClient,
		comStmtPrepare("UPDATE accounts SET balance = ? WHERE id = ?"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	if stmts[0].Operation != hoopinspect.OpUpdate {
		t.Errorf("Operation = %q, want update", stmts[0].Operation)
	}
	if stmts[0].Metadata["mysql.command"] != "COM_STMT_PREPARE" {
		t.Errorf("mysql.command = %q, want COM_STMT_PREPARE", stmts[0].Metadata["mysql.command"])
	}
}

// The client's handshake response is not a command. Its first byte (0x03 in a
// typical capability flag set) collides with COM_QUERY, so a decoder that
// skipped the phase check would emit a garbage "statement" from auth bytes.
func TestHandshakeResponseIsNotAStatement(t *testing.T) {
	insp := newInspector(t)

	stmts, err := insp.Inspect(hoopinspect.FromClient, handshakeResponse())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 0 {
		t.Fatalf("handshake response decoded as %d statements: %+v", len(stmts), stmts)
	}

	stmts, err = insp.Inspect(hoopinspect.FromClient, comQuery("SELECT 1"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements after handshake, want 1", len(stmts))
	}
	if stmts[0].Text != "SELECT 1" {
		t.Errorf("Text = %q, want SELECT 1", stmts[0].Text)
	}
}

// Non-SQL commands must not produce statements.
func TestNonSQLCommandsIgnored(t *testing.T) {
	for name, payload := range map[string][]byte{
		"COM_QUIT":       {0x01},
		"COM_PING":       {0x0e},
		"COM_INIT_DB":    append([]byte{0x02}, "appdb"...),
		"COM_STMT_CLOSE": {0x19, 1, 0, 0, 0},
	} {
		insp := newInspector(t)
		stmts, err := insp.Inspect(hoopinspect.FromClient, packet(0, payload))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(stmts) != 0 {
			t.Errorf("%s produced %d statements, want 0", name, len(stmts))
		}
	}
}

func TestSplitAcrossReads(t *testing.T) {
	full := comQuery("DELETE FROM customers WHERE id = 1")

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
		if got[0].Operation != hoopinspect.OpDelete {
			t.Errorf("split=%d: Operation = %q, want delete", split, got[0].Operation)
		}
	}
}

func TestMultipleCommandsInOneRead(t *testing.T) {
	insp := newInspector(t)
	stream := append(comQuery("SELECT 1"), comQuery("DROP TABLE t")...)

	stmts, err := insp.Inspect(hoopinspect.FromClient, stream)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want 2", len(stmts))
	}
	if stmts[1].Operation != hoopinspect.OpDrop {
		t.Errorf("stmts[1].Operation = %q, want drop", stmts[1].Operation)
	}
}

func TestSkipHandshake(t *testing.T) {
	c := &mysql.Codec{}
	c.SkipHandshake()
	insp := hoopinspect.NewWithCodec(c)

	// With the handshake marked done, a seq-0 command decodes immediately.
	stmts, err := insp.Inspect(hoopinspect.FromClient, comQuery("SELECT 1"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
}

func TestSequenceAndPayloadLen(t *testing.T) {
	p := packet(7, []byte{0x03, 'x'})

	seq, ok := mysql.Sequence(p)
	if !ok || seq != 7 {
		t.Errorf("Sequence = (%d, %v), want (7, true)", seq, ok)
	}
	n, ok := mysql.PayloadLen(p)
	if !ok || n != 2 {
		t.Errorf("PayloadLen = (%d, %v), want (2, true)", n, ok)
	}
	if _, ok := mysql.Sequence([]byte{1, 2}); ok {
		t.Error("Sequence accepted a truncated header")
	}
}

func TestServerDirectionYieldsNothing(t *testing.T) {
	insp := newInspector(t)
	stmts, err := insp.Inspect(hoopinspect.FromServer, comQuery("SELECT 1"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 0 {
		t.Errorf("got %d statements from server direction, want 0", len(stmts))
	}
}

func TestInspectorsDoNotShareState(t *testing.T) {
	a := newInspector(t)
	b := newInspector(t)

	// Leave `a` mid-packet.
	partial := comQuery("DELETE FROM secrets")[:6]
	if _, err := a.Inspect(hoopinspect.FromClient, partial); err != nil {
		t.Fatalf("a: %v", err)
	}

	stmts, err := b.Inspect(hoopinspect.FromClient, comQuery("SELECT 1"))
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if len(stmts) != 1 || stmts[0].Text != "SELECT 1" {
		t.Errorf("b saw %+v — state leaked between inspectors", stmts)
	}
}
