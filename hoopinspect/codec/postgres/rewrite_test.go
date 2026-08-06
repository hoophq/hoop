package postgres_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	pg "github.com/hoophq/hoopinspect/codec/postgres"
)

// rewriteAll runs a whole stream through one codec and returns everything it
// emitted, flush included, the way the relay does across a connection.
func rewriteAll(t *testing.T, stream []byte, mask func(string, []byte) []byte) ([]byte, hoopinspect.ReframeResult) {
	t.Helper()
	c := &pg.Codec{}
	out, res, err := c.Rewrite(stream, mask)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	return append(out, c.Flush(mask)...), res
}

// mustReparse is the assertion that matters: a client reading this stream
// must not desynchronize. A wrong length prefix surfaces here exactly as
// psql's "lost synchronization with server".
func mustReparse(t *testing.T, stream []byte) []hoopinspect.Statement {
	t.Helper()
	insp := hoopinspect.NewWithCodec(&pg.Codec{})
	stmts, err := insp.Inspect(hoopinspect.FromServer, stream)
	if err != nil {
		t.Fatalf("rewritten stream is not valid pgwire: %v", err)
	}
	return stmts
}

func redactColumn(target string) func(string, []byte) []byte {
	return func(col string, v []byte) []byte {
		if col == target {
			return []byte("[REDACTED]")
		}
		return v
	}
}

// --- framing ----------------------------------------------------------------

// A replacement of a DIFFERENT length must leave the stream parseable. Both
// directions, because a shrink corrupts as thoroughly as a growth.
func TestRewriteReframesGrowAndShrink(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("name", "ssn"),
		dataRowMsg(str("Ada Lovelace"), str("123-45-6789")),
		dataRowMsg(str("Grace Hopper"), str("987-65-4321")),
		commandComplete("SELECT 2"),
		readyForQuery(),
	}, nil)

	for _, tc := range []struct {
		name    string
		replace string
	}{
		{"grows", "[REDACTED:US_SSN]"},
		{"shrinks", "x"},
		{"same length", "***-**-****"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, res := rewriteAll(t, stream, func(col string, v []byte) []byte {
				if col == "ssn" {
					return []byte(tc.replace)
				}
				return v
			})

			if res.Cells != 2 {
				t.Errorf("Cells = %d, want 2", res.Cells)
			}
			if bytes.Contains(out, []byte("123-45-6789")) {
				t.Error("value survived")
			}
			if !bytes.Contains(out, []byte("Ada Lovelace")) {
				t.Error("an untouched column was corrupted")
			}

			stmts := mustReparse(t, out)
			if len(stmts) != 1 || stmts[0].Result.RowCount != 2 {
				t.Fatalf("re-framed stream lost rows: %+v", stmts)
			}
		})
	}
}

// NULL is length -1, not a zero-length value. Re-encoding it as empty would
// change the data the client sees.
func TestRewritePreservesNull(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("name", "ssn"),
		dataRowMsg(nil, str("123-45-6789")),
		commandComplete("SELECT 1"),
	}, nil)

	out, _ := rewriteAll(t, stream, redactColumn("ssn"))
	mustReparse(t, out)

	// Find the DataRow and check the first column is still the NULL sentinel.
	i := bytes.IndexByte(out, 'D')
	if i < 0 {
		t.Fatal("no DataRow in output")
	}
	firstLen := binary.BigEndian.Uint32(out[i+7 : i+11])
	if firstLen != 0xFFFFFFFF {
		t.Errorf("NULL was re-encoded as length %d, not -1", firstLen)
	}
}

// A masker that changes nothing must leave the bytes byte-identical. Most
// result sets hold no sensitive values, and rewriting one is pure cost.
func TestRewriteUnchangedIsByteIdentical(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("a", "b"),
		dataRowMsg(str("1"), str("2")),
		commandComplete("SELECT 1"),
		readyForQuery(),
	}, nil)

	out, res := rewriteAll(t, stream, func(_ string, v []byte) []byte { return v })
	if res.Cells != 0 {
		t.Errorf("Cells = %d on an unchanged stream", res.Cells)
	}
	if !bytes.Equal(out, stream) {
		t.Error("an unchanged stream was rewritten")
	}
}

// Message ORDER drives the client's state machine. Rows are buffered, so the
// danger is emitting them after the CommandComplete that ends them.
func TestRewritePreservesMessageOrder(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("ssn"),
		dataRowMsg(str("123-45-6789")),
		commandComplete("SELECT 1"),
		readyForQuery(),
	}, nil)

	out, _ := rewriteAll(t, stream, redactColumn("ssn"))

	iRow := bytes.IndexByte(out, 'D')
	iDone := bytes.Index(out, []byte("SELECT 1"))
	iReady := bytes.LastIndexByte(out, 'Z')
	if !(iRow < iDone && iDone < iReady) {
		t.Errorf("messages out of order: row=%d complete=%d ready=%d", iRow, iDone, iReady)
	}
}

// The relay hands the codec whatever the socket returned, which has nothing to
// do with message boundaries. Every split must produce the same bytes.
func TestRewriteSplitReadMatrix(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("name", "ssn"),
		dataRowMsg(str("Ada Lovelace"), str("123-45-6789")),
		dataRowMsg(str("Grace Hopper"), str("987-65-4321")),
		commandComplete("SELECT 2"),
		readyForQuery(),
	}, nil)

	whole, _ := rewriteAll(t, stream, redactColumn("ssn"))

	for cut := 0; cut <= len(stream); cut++ {
		c := &pg.Codec{}
		var out []byte
		for _, chunk := range [][]byte{stream[:cut], stream[cut:]} {
			if len(chunk) == 0 {
				continue
			}
			got, _, err := c.Rewrite(chunk, redactColumn("ssn"))
			if err != nil {
				t.Fatalf("cut=%d: %v", cut, err)
			}
			out = append(out, got...)
		}
		out = append(out, c.Flush(redactColumn("ssn"))...)

		if !bytes.Equal(out, whole) {
			t.Fatalf("cut=%d produced different bytes than the unsplit stream", cut)
		}
		if bytes.Contains(out, []byte("123-45-6789")) {
			t.Fatalf("cut=%d: value survived", cut)
		}
	}
}

// Rows held when the connection ends must still reach the client. Dropping
// them truncates the result set, worse than masking late.
func TestFlushReleasesHeldRows(t *testing.T) {
	// No terminator: the rows are still buffered when the peer goes away.
	stream := bytes.Join([][]byte{
		rowDescMsg("ssn"),
		dataRowMsg(str("123-45-6789")),
	}, nil)

	c := &pg.Codec{}
	out, _, err := c.Rewrite(stream, redactColumn("ssn"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte{'D'}) {
		t.Error("a row was forwarded before its result set ended")
	}

	tail := c.Flush(redactColumn("ssn"))
	if !bytes.Contains(tail, []byte("[REDACTED]")) {
		t.Errorf("Flush lost the held row: %q", tail)
	}
	mustReparse(t, append(out, tail...))
}

// A nil masker is a pass-through, and must not hold bytes back.
func TestRewriteNilMaskerPassesThrough(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("ssn"),
		dataRowMsg(str("123-45-6789")),
		commandComplete("SELECT 1"),
	}, nil)

	c := &pg.Codec{}
	out, res, err := c.Rewrite(stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, stream) || res.Cells != 0 {
		t.Error("a nil masker changed the stream")
	}
}

// Column names come from the RowDescription and must reach the masker. They
// are the reason a column rule can beat a pattern.
func TestMaskerSeesColumnNames(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("first", "second"),
		dataRowMsg(str("a"), str("b")),
		commandComplete("SELECT 1"),
	}, nil)

	var seen []string
	rewriteAll(t, stream, func(col string, v []byte) []byte {
		seen = append(seen, col)
		return v
	})

	if strings.Join(seen, ",") != "first,second" {
		t.Errorf("columns seen = %v, want [first second]", seen)
	}
}

// A second result set re-describes itself; the masker must not be shown the
// previous query's column names.
func TestColumnNamesResetBetweenResultSets(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("alpha"),
		dataRowMsg(str("1")),
		commandComplete("SELECT 1"),
		readyForQuery(),
		rowDescMsg("beta"),
		dataRowMsg(str("2")),
		commandComplete("SELECT 1"),
	}, nil)

	var seen []string
	rewriteAll(t, stream, func(col string, v []byte) []byte {
		seen = append(seen, col)
		return v
	})

	if strings.Join(seen, ",") != "alpha,beta" {
		t.Errorf("columns seen = %v, want [alpha beta]", seen)
	}
}

// A masker returning nil must be read as "unchanged", never as "delete the
// cell": deleting one would shift every column after it.
func TestNilReplacementLeavesCellIntact(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("a"),
		dataRowMsg(str("keep me")),
		commandComplete("SELECT 1"),
	}, nil)

	out, res := rewriteAll(t, stream, func(string, []byte) []byte { return nil })
	if res.Cells != 0 {
		t.Errorf("Cells = %d, want 0", res.Cells)
	}
	if !bytes.Contains(out, []byte("keep me")) {
		t.Error("a nil replacement dropped the value")
	}
	mustReparse(t, out)
}

// Non-DataRow messages must pass through untouched even while rows are held.
func TestOtherMessagesPassThrough(t *testing.T) {
	notice := backendMsg('N', append([]byte("SNOTICE\x00"), 0))
	stream := bytes.Join([][]byte{
		rowDescMsg("ssn"),
		dataRowMsg(str("123-45-6789")),
		notice,
		commandComplete("SELECT 1"),
	}, nil)

	out, _ := rewriteAll(t, stream, redactColumn("ssn"))
	if !bytes.Contains(out, []byte("SNOTICE")) {
		t.Error("a NoticeResponse was swallowed")
	}
	mustReparse(t, out)
}

// A row wider than the description must not panic. The masker is shown an
// empty column name for the extra cells rather than reading past the slice.
func TestMoreColumnsThanDescribed(t *testing.T) {
	stream := bytes.Join([][]byte{
		rowDescMsg("only"),
		dataRowMsg(str("a"), str("b"), str("c")),
		commandComplete("SELECT 1"),
	}, nil)

	var seen []string
	out, _ := rewriteAll(t, stream, func(col string, v []byte) []byte {
		seen = append(seen, col)
		return v
	})
	if len(seen) != 3 {
		t.Errorf("saw %d cells, want 3", len(seen))
	}
	if seen[1] != "" || seen[2] != "" {
		t.Errorf("undescribed columns should be unnamed, got %v", seen)
	}
	mustReparse(t, out)
}
