package postgres_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/hoophq/hoopinspect"
	pg "github.com/hoophq/hoopinspect/codec/postgres"
)

// --- fixtures ---------------------------------------------------------------

// backendMsg frames a payload as a tagged backend message.
func backendMsg(tag byte, payload []byte) []byte {
	out := make([]byte, 0, 5+len(payload))
	out = append(out, tag)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)+4))
	return append(out, payload...)
}

// rowDescMsg builds a RowDescription naming the given columns, all text type.
func rowDescMsg(names ...string) []byte {
	var p []byte
	p = binary.BigEndian.AppendUint16(p, uint16(len(names)))
	for _, n := range names {
		p = append(p, n...)
		p = append(p, 0)
		p = append(p, make([]byte, 6)...)        // table OID + attr num
		p = binary.BigEndian.AppendUint32(p, 25) // data type OID: text
		p = append(p, make([]byte, 8)...)        // size, modifier, format
	}
	return backendMsg('T', p)
}

// dataRowMsg builds a DataRow. A nil value encodes SQL NULL.
func dataRowMsg(vals ...[]byte) []byte {
	var p []byte
	p = binary.BigEndian.AppendUint16(p, uint16(len(vals)))
	for _, v := range vals {
		if v == nil {
			p = binary.BigEndian.AppendUint32(p, 0xFFFFFFFF)
			continue
		}
		p = binary.BigEndian.AppendUint32(p, uint32(len(v)))
		p = append(p, v...)
	}
	return backendMsg('D', p)
}

func commandComplete(tag string) []byte { return backendMsg('C', append([]byte(tag), 0)) }
func readyForQuery() []byte             { return backendMsg('Z', []byte{'I'}) }

func str(s string) []byte { return []byte(s) }

// --- decode -----------------------------------------------------------------

func TestDecodesResultSetShape(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("name", "ssn"),
		dataRowMsg(str("Ada Lovelace"), str("123-45-6789")),
		dataRowMsg(str("Grace Hopper"), nil),
		commandComplete("SELECT 2"),
		readyForQuery(),
	}, nil)

	stmts := inspectServer(t, stream)
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	s := stmts[0]

	if s.Direction != hoopinspect.FromServer {
		t.Errorf("Direction = %v", s.Direction)
	}
	if s.Text != "SELECT 2" {
		t.Errorf("Text = %q, want the command tag", s.Text)
	}
	if s.Result == nil {
		t.Fatal("no Result")
	}
	if s.Result.RowCount != 2 {
		t.Errorf("RowCount = %d, want 2", s.Result.RowCount)
	}
	if len(s.Result.Columns) != 2 ||
		s.Result.Columns[0].Name != "name" || s.Result.Columns[1].Name != "ssn" {
		t.Errorf("Columns = %+v", s.Result.Columns)
	}
	if s.Result.Columns[1].DataTypeOID != 25 {
		t.Errorf("DataTypeOID = %d, want 25", s.Result.Columns[1].DataTypeOID)
	}
}

// A Statement is an audit record. Carrying the cells would write the values
// masking exists to remove straight into the log.
func TestResultCarriesNoCellValues(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("ssn"),
		dataRowMsg(str("123-45-6789")),
		commandComplete("SELECT 1"),
	}, nil)

	for _, s := range inspectServer(t, stream) {
		if bytes.Contains([]byte(s.String()), []byte("123-45-6789")) {
			t.Errorf("a cell value reached the statement: %s", s.String())
		}
	}
}

// An error terminates a result set too, and must be distinguishable from a
// query that returned nothing.
func TestErrorResponseIsRecorded(t *testing.T) {
	stream := bytes.Join([][]byte{
		backendMsg('E', append([]byte("SFATAL\x00Mpermission denied\x00"), 0)),
		readyForQuery(),
	}, nil)

	stmts := inspectServer(t, stream)
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	if got := stmts[0].Metadata["pg.message"]; got != "ErrorResponse" {
		t.Errorf("pg.message = %q, want ErrorResponse", got)
	}
}

// Two queries on one connection are two result sets, never one merged blob.
func TestConsecutiveResultSets(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("a"),
		dataRowMsg(str("1")),
		commandComplete("SELECT 1"),
		readyForQuery(),
		rowDescMsg("b", "c"),
		dataRowMsg(str("2"), str("3")),
		dataRowMsg(str("4"), str("5")),
		commandComplete("SELECT 2"),
		readyForQuery(),
	}, nil)

	stmts := inspectServer(t, stream)
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want 2", len(stmts))
	}
	if stmts[0].Result.RowCount != 1 || len(stmts[0].Result.Columns) != 1 {
		t.Errorf("first result set: %+v", stmts[0].Result)
	}
	if stmts[1].Result.RowCount != 2 || len(stmts[1].Result.Columns) != 2 {
		t.Errorf("second result set: %+v", stmts[1].Result)
	}
}

// The split-read matrix every codec in this library must pass: the same
// stream fed in two chunks at every possible boundary produces the same
// result, with nothing emitted from a fragment.
func TestResponseSplitReadMatrix(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("name", "ssn"),
		dataRowMsg(str("Ada Lovelace"), str("123-45-6789")),
		dataRowMsg(str("Grace Hopper"), str("987-65-4321")),
		commandComplete("SELECT 2"),
		readyForQuery(),
	}, nil)

	for cut := 0; cut <= len(stream); cut++ {
		insp := hoopinspect.NewWithCodec(&pg.Codec{})

		var stmts []hoopinspect.Statement
		for _, chunk := range [][]byte{stream[:cut], stream[cut:]} {
			if len(chunk) == 0 {
				continue
			}
			got, err := insp.Inspect(hoopinspect.FromServer, chunk)
			if err != nil {
				t.Fatalf("cut=%d: %v", cut, err)
			}
			stmts = append(stmts, got...)
		}

		if len(stmts) != 1 {
			t.Fatalf("cut=%d: got %d statements, want exactly 1", cut, len(stmts))
		}
		if stmts[0].Result.RowCount != 2 {
			t.Errorf("cut=%d: RowCount = %d, want 2", cut, stmts[0].Result.RowCount)
		}
		if len(stmts[0].Result.Columns) != 2 {
			t.Errorf("cut=%d: lost the column description", cut)
		}
	}
}

// A request-direction stream must be unaffected by any of this.
func TestRequestStillDecodes(t *testing.T) {
	q := backendMsg('Q', append([]byte("SELECT 1"), 0))

	insp := hoopinspect.NewWithCodec(&pg.Codec{})
	stmts, err := insp.Inspect(hoopinspect.FromClient, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != 1 || stmts[0].Text != "SELECT 1" {
		t.Fatalf("got %+v", stmts)
	}
	if stmts[0].Result != nil {
		t.Error("a request carries no Result")
	}
}

// Garbage must be reported rather than consumed: a desynchronized stream
// makes every later offset wrong.
func TestMalformedResponseRejected(t *testing.T) {
	// Declared length of 2 is below the legal minimum of 4.
	bad := []byte{'D', 0, 0, 0, 2, 0, 0}
	insp := hoopinspect.NewWithCodec(&pg.Codec{})
	if _, err := insp.Inspect(hoopinspect.FromServer, bad); err == nil {
		t.Fatal("malformed response accepted")
	}
}

// A DataRow whose column prefixes do not span the payload is malformed.
func TestMalformedDataRowRejected(t *testing.T) {
	var p []byte
	p = binary.BigEndian.AppendUint16(p, 1)  // one column
	p = binary.BigEndian.AppendUint32(p, 99) // claims 99 bytes
	p = append(p, "short"...)                // provides 5

	insp := hoopinspect.NewWithCodec(&pg.Codec{})
	if _, err := insp.Inspect(hoopinspect.FromServer, backendMsg('D', p)); err == nil {
		t.Fatal("a row whose lengths overrun the payload was accepted")
	}
}

func inspectServer(t *testing.T, stream []byte) []hoopinspect.Statement {
	t.Helper()
	insp := hoopinspect.NewWithCodec(&pg.Codec{})
	stmts, err := insp.Inspect(hoopinspect.FromServer, stream)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return stmts
}
