package mongodb_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	_ "github.com/hoophq/hoopinspect/codec/mongodb"
)

// --- minimal BSON writer, for building fixtures --------------------------

type doc struct{ buf bytes.Buffer }

func (d *doc) str(key, val string) *doc {
	d.buf.WriteByte(0x02)
	d.buf.WriteString(key)
	d.buf.WriteByte(0)
	binary.Write(&d.buf, binary.LittleEndian, uint32(len(val)+1))
	d.buf.WriteString(val)
	d.buf.WriteByte(0)
	return d
}

func (d *doc) i32(key string, val int32) *doc {
	d.buf.WriteByte(0x10)
	d.buf.WriteString(key)
	d.buf.WriteByte(0)
	binary.Write(&d.buf, binary.LittleEndian, val)
	return d
}

func (d *doc) boolean(key string, val bool) *doc {
	d.buf.WriteByte(0x08)
	d.buf.WriteString(key)
	d.buf.WriteByte(0)
	if val {
		d.buf.WriteByte(1)
	} else {
		d.buf.WriteByte(0)
	}
	return d
}

func (d *doc) sub(key string, inner *doc) *doc {
	d.buf.WriteByte(0x03)
	d.buf.WriteString(key)
	d.buf.WriteByte(0)
	d.buf.Write(inner.bytes())
	return d
}

func (d *doc) bytes() []byte {
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint32(d.buf.Len()+5))
	out.Write(d.buf.Bytes())
	out.WriteByte(0)
	return out.Bytes()
}

// opMsg wraps a command document in an OP_MSG message.
func opMsg(body []byte) []byte {
	var section bytes.Buffer
	binary.Write(&section, binary.LittleEndian, uint32(0)) // flagBits
	section.WriteByte(0)                                   // kind 0
	section.Write(body)

	var msg bytes.Buffer
	binary.Write(&msg, binary.LittleEndian, uint32(16+section.Len()))
	binary.Write(&msg, binary.LittleEndian, uint32(1))    // requestID
	binary.Write(&msg, binary.LittleEndian, uint32(0))    // responseTo
	binary.Write(&msg, binary.LittleEndian, uint32(2013)) // OP_MSG
	msg.Write(section.Bytes())
	return msg.Bytes()
}

// opQuery wraps a document in a legacy OP_QUERY message.
func opQuery(collection string, body []byte) []byte {
	var section bytes.Buffer
	binary.Write(&section, binary.LittleEndian, uint32(0)) // flags
	section.WriteString(collection)
	section.WriteByte(0)
	binary.Write(&section, binary.LittleEndian, uint32(0)) // numberToSkip
	binary.Write(&section, binary.LittleEndian, uint32(1)) // numberToReturn
	section.Write(body)

	var msg bytes.Buffer
	binary.Write(&msg, binary.LittleEndian, uint32(16+section.Len()))
	binary.Write(&msg, binary.LittleEndian, uint32(1))
	binary.Write(&msg, binary.LittleEndian, uint32(0))
	binary.Write(&msg, binary.LittleEndian, uint32(2004)) // OP_QUERY
	msg.Write(section.Bytes())
	return msg.Bytes()
}

func newInspector(t *testing.T) *hoopinspect.Inspector {
	t.Helper()
	i, err := hoopinspect.New(hoopinspect.MongoDB)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return i
}

func TestCommandClassification(t *testing.T) {
	tests := []struct {
		name     string
		build    func() []byte
		wantCmd  string
		wantOp   hoopinspect.Operation
		wantColl string
	}{
		{
			"find", func() []byte {
				return (&doc{}).str("find", "customers").str("$db", "appdb").bytes()
			},
			"find", hoopinspect.OpFind, "customers",
		},
		{
			"delete", func() []byte {
				return (&doc{}).str("delete", "customers").str("$db", "appdb").bytes()
			},
			"delete", hoopinspect.OpDelete, "customers",
		},
		{
			"insert", func() []byte {
				return (&doc{}).str("insert", "orders").str("$db", "appdb").bytes()
			},
			"insert", hoopinspect.OpInsert, "orders",
		},
		{
			"update", func() []byte {
				return (&doc{}).str("update", "accounts").str("$db", "appdb").bytes()
			},
			"update", hoopinspect.OpUpdate, "accounts",
		},
		{
			"drop", func() []byte {
				return (&doc{}).str("drop", "staging").str("$db", "appdb").bytes()
			},
			"drop", hoopinspect.OpDrop, "staging",
		},
		{
			"aggregate reads", func() []byte {
				return (&doc{}).str("aggregate", "events").str("$db", "appdb").bytes()
			},
			"aggregate", hoopinspect.OpFind, "events",
		},
		{
			"hello is admin", func() []byte {
				return (&doc{}).i32("hello", 1).str("$db", "admin").bytes()
			},
			"hello", hoopinspect.OpAdmin, "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			insp := newInspector(t)
			stmts, err := insp.Inspect(hoopinspect.FromClient, opMsg(tc.build()))
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if len(stmts) != 1 {
				t.Fatalf("got %d statements, want 1: %+v", len(stmts), stmts)
			}
			s := stmts[0]
			if s.Metadata["mongo.command"] != tc.wantCmd {
				t.Errorf("mongo.command = %q, want %q", s.Metadata["mongo.command"], tc.wantCmd)
			}
			if s.Operation != tc.wantOp {
				t.Errorf("Operation = %q, want %q", s.Operation, tc.wantOp)
			}
			if s.Metadata["mongo.collection"] != tc.wantColl {
				t.Errorf("mongo.collection = %q, want %q", s.Metadata["mongo.collection"], tc.wantColl)
			}
			if tc.wantColl != "" {
				if len(s.Tables) != 1 || s.Tables[0] != tc.wantColl {
					t.Errorf("Tables = %v, want [%s]", s.Tables, tc.wantColl)
				}
			}
		})
	}
}

// An unrecognized command must classify as OpOther, never as a read. A new
// server command defaulting to "safe" is how a policy gets bypassed.
func TestUnknownCommandIsNotARead(t *testing.T) {
	insp := newInspector(t)
	body := (&doc{}).str("someFutureCommand", "customers").bytes()

	stmts, err := insp.Inspect(hoopinspect.FromClient, opMsg(body))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if stmts[0].Operation != hoopinspect.OpOther {
		t.Errorf("Operation = %q, want other", stmts[0].Operation)
	}
	if stmts[0].Operation == hoopinspect.OpFind {
		t.Error("unknown command classified as a read")
	}
}

func TestDatabaseExtracted(t *testing.T) {
	insp := newInspector(t)
	body := (&doc{}).str("find", "customers").str("$db", "appdb").bytes()

	stmts, err := insp.Inspect(hoopinspect.FromClient, opMsg(body))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if stmts[0].Database != "appdb" {
		t.Errorf("Database = %q, want appdb", stmts[0].Database)
	}
}

func TestOpQueryHandshake(t *testing.T) {
	insp := newInspector(t)
	body := (&doc{}).i32("isMaster", 1).bytes()

	stmts, err := insp.Inspect(hoopinspect.FromClient, opQuery("admin.$cmd", body))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	if stmts[0].Metadata["mongo.opcode"] != "OP_QUERY" {
		t.Errorf("mongo.opcode = %q, want OP_QUERY", stmts[0].Metadata["mongo.opcode"])
	}
	if stmts[0].Operation != hoopinspect.OpAdmin {
		t.Errorf("Operation = %q, want admin", stmts[0].Operation)
	}
}

func TestTextIsJSON(t *testing.T) {
	insp := newInspector(t)
	body := (&doc{}).
		str("find", "customers").
		i32("limit", 10).
		boolean("singleBatch", true).
		sub("filter", (&doc{}).str("email", "a@b.c")).
		str("$db", "appdb").
		bytes()

	stmts, err := insp.Inspect(hoopinspect.FromClient, opMsg(body))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	text := stmts[0].Text

	for _, want := range []string{
		`"find":"customers"`,
		`"limit":10`,
		`"singleBatch":true`,
		`"filter":{"email":"a@b.c"}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Text %q missing %q", text, want)
		}
	}
	if !strings.HasPrefix(text, "{") || !strings.HasSuffix(text, "}") {
		t.Errorf("Text is not a JSON object: %q", text)
	}
}

func TestSplitAcrossReads(t *testing.T) {
	body := (&doc{}).str("delete", "customers").str("$db", "appdb").bytes()
	full := opMsg(body)

	for split := 1; split < len(full); split++ {
		insp := newInspector(t)

		if got, err := insp.Inspect(hoopinspect.FromClient, full[:split]); err != nil {
			t.Fatalf("split=%d first: %v", split, err)
		} else if len(got) != 0 {
			t.Fatalf("split=%d: emitted from a partial message", split)
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

func TestMultipleMessagesInOneRead(t *testing.T) {
	insp := newInspector(t)
	a := opMsg((&doc{}).str("find", "customers").bytes())
	b := opMsg((&doc{}).str("drop", "staging").bytes())

	stmts, err := insp.Inspect(hoopinspect.FromClient, append(a, b...))
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

// A truncated BSON document must not panic the walker; malformed input from a
// hostile client cannot be allowed to take the process down.
func TestTruncatedBSONDoesNotPanic(t *testing.T) {
	body := (&doc{}).str("find", "customers").str("$db", "appdb").bytes()
	full := opMsg(body)

	for cut := len(full) - 1; cut > 16; cut-- {
		truncated := make([]byte, cut)
		copy(truncated, full)
		// Keep the declared length honest so the decoder tries to parse.
		binary.LittleEndian.PutUint32(truncated, uint32(cut))

		insp := newInspector(t)
		// Must not panic; an error or an empty result are both acceptable.
		_, _ = insp.Inspect(hoopinspect.FromClient, truncated)
	}
}

func TestMalformedLengthErrors(t *testing.T) {
	insp := newInspector(t)
	bad := make([]byte, 16)
	binary.LittleEndian.PutUint32(bad, 4) // shorter than the header
	if _, err := insp.Inspect(hoopinspect.FromClient, bad); err == nil {
		t.Fatal("expected an error for a length below the header size")
	}
}

func TestServerDirectionYieldsNothing(t *testing.T) {
	insp := newInspector(t)
	body := (&doc{}).str("find", "customers").bytes()

	stmts, err := insp.Inspect(hoopinspect.FromServer, opMsg(body))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(stmts) != 0 {
		t.Errorf("got %d statements from server direction, want 0", len(stmts))
	}
}
