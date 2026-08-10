package mssql_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/codec/mssql"
)

// --- builders for a TDS result set -----------------------------------------

func colMetaNVarChar(names ...string) []byte {
	var b bytes.Buffer
	b.WriteByte(0x81)
	binary.Write(&b, binary.LittleEndian, uint16(len(names)))
	for _, n := range names {
		binary.Write(&b, binary.LittleEndian, uint32(0)) // UserType
		binary.Write(&b, binary.LittleEndian, uint16(0)) // Flags
		b.WriteByte(0xe7)                                // NVARCHAR
		binary.Write(&b, binary.LittleEndian, uint16(8000))
		b.Write(make([]byte, 5)) // collation
		b.WriteByte(byte(len([]rune(n))))
		b.Write(ucs2(n))
	}
	return b.Bytes()
}

func rowNVarChar(vals ...string) []byte {
	var b bytes.Buffer
	b.WriteByte(0xd1)
	for _, v := range vals {
		enc := ucs2(v)
		binary.Write(&b, binary.LittleEndian, uint16(len(enc)))
		b.Write(enc)
	}
	return b.Bytes()
}

func doneToken() []byte {
	b := []byte{0xfd}
	b = binary.LittleEndian.AppendUint16(b, 0)
	b = binary.LittleEndian.AppendUint16(b, 0)
	return binary.LittleEndian.AppendUint64(b, 0)
}

// wrap lays TDS packet headers over a token stream, splitting at `at` to force
// a token across a packet boundary when at > 0.
func wrap(stream []byte, at int) []byte {
	var out []byte
	emit := func(chunk []byte, eom bool) {
		h := make([]byte, 8)
		h[0] = 0x04
		if eom {
			h[1] = 0x01
		}
		binary.BigEndian.PutUint16(h[2:4], uint16(8+len(chunk)))
		h[6] = 1
		out = append(out, h...)
		out = append(out, chunk...)
	}
	if at <= 0 || at >= len(stream) {
		emit(stream, true)
		return out
	}
	emit(stream[:at], false)
	emit(stream[at:], true)
	return out
}

// tokens strips packet headers so a test can read the result.
func tokens(t *testing.T, b []byte) []byte {
	t.Helper()
	var out []byte
	for p := 0; p+8 <= len(b); {
		n := int(binary.BigEndian.Uint16(b[p+2 : p+4]))
		if n < 8 || p+n > len(b) {
			t.Fatalf("malformed packet at %d", p)
		}
		out = append(out, b[p+8:p+n]...)
		p += n
	}
	return out
}

func redactEmail(col string, v []byte) []byte {
	if strings.Contains(string(v), "@") {
		return []byte("[REDACTED]")
	}
	return v
}

// --- the tests --------------------------------------------------------------

// The point of the whole exercise: a value leaves masked, and the frame around
// it is rebuilt so the client stays in sync.
func TestRewriteMasksACell(t *testing.T) {
	c := &mssql.Codec{}
	stream := append(colMetaNVarChar("name", "email"), rowNVarChar("Ada", "ada@example.com")...)
	stream = append(stream, doneToken()...)

	out, res, err := c.Rewrite(wrap(stream, 0), redactEmail)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.Cells != 1 || res.Rows != 1 {
		t.Fatalf("res = %+v, want 1 cell in 1 row", res)
	}
	got := tokens(t, out)
	if !bytes.Contains(got, ucs2("[REDACTED]")) {
		t.Error("masked value is not in the output")
	}
	if bytes.Contains(got, ucs2("ada@example.com")) {
		t.Error("the original value survived")
	}
	if !bytes.Contains(got, ucs2("Ada")) {
		t.Error("an unmasked column was lost")
	}
}

// The rebuilt row must declare the NEW length. A stale prefix is what
// desynchronizes a client, and it is invisible unless the frame is re-read.
func TestRewrittenRowDeclaresTheNewLength(t *testing.T) {
	c := &mssql.Codec{}
	stream := append(colMetaNVarChar("email"), rowNVarChar("ada@example.com")...)
	out, _, err := c.Rewrite(wrap(stream, 0), redactEmail)
	if err != nil {
		t.Fatal(err)
	}
	got := tokens(t, out)

	i := bytes.Index(got, []byte{0xd1})
	if i < 0 {
		t.Fatal("no ROW token in the output")
	}
	declared := binary.LittleEndian.Uint16(got[i+1 : i+3])
	want := uint16(len(ucs2("[REDACTED]")))
	if declared != want {
		t.Errorf("declared length = %d, want %d", declared, want)
	}
	if int(declared) != len(got)-(i+3) {
		t.Errorf("declared %d but %d bytes follow", declared, len(got)-(i+3))
	}
}

// A row split across two packets must be reassembled before rewriting. Reading
// the kernel's chunking as a token boundary would corrupt every later byte.
func TestRowSplitAcrossPacketsIsReassembled(t *testing.T) {
	c := &mssql.Codec{}
	stream := append(colMetaNVarChar("email"), rowNVarChar("ada@example.com")...)
	stream = append(stream, doneToken()...)
	framed := wrap(stream, len(stream)/2)

	var all []byte
	for i := 0; i < len(framed); i++ {
		out, _, err := c.Rewrite(framed[i:i+1], redactEmail)
		if err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		all = append(all, out...)
	}
	all = append(all, c.Flush(redactEmail)...)

	got := tokens(t, all)
	if !bytes.Contains(got, ucs2("[REDACTED]")) {
		t.Error("a row split across packets was not masked")
	}
}

// NULLs carry no bytes under NBCROW; miscounting the bitmap shifts every value.
func TestNBCRowNullsAreSkipped(t *testing.T) {
	c := &mssql.Codec{}
	var row bytes.Buffer
	row.WriteByte(0xd2)
	row.WriteByte(0x02) // column 1 (email) is NULL
	row.Write(ucs2Len("Ada"))

	stream := append(colMetaNVarChar("name", "email"), row.Bytes()...)
	out, res, err := c.Rewrite(wrap(stream, 0), redactEmail)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.Cells != 0 {
		t.Errorf("masked %d cells, want 0: the only text value is NULL", res.Cells)
	}
	if !bytes.Contains(tokens(t, out), ucs2("Ada")) {
		t.Error("the non-null value was lost")
	}
}

// A type this codec cannot measure must stop rewriting, not guess. Guessing
// desynchronizes the client, which is worse than an unmasked value.
func TestUnmeasurableTypeStopsRewritingWithoutCorrupting(t *testing.T) {
	c := &mssql.Codec{}
	var meta bytes.Buffer
	meta.WriteByte(0x81)
	binary.Write(&meta, binary.LittleEndian, uint16(1))
	binary.Write(&meta, binary.LittleEndian, uint32(0))
	binary.Write(&meta, binary.LittleEndian, uint16(0))
	meta.WriteByte(0x62) // SQL_VARIANT
	binary.Write(&meta, binary.LittleEndian, uint32(8009))
	meta.WriteByte(1)
	meta.Write(ucs2("v"))

	in := wrap(meta.Bytes(), 0)
	out, res, err := c.Rewrite(in, redactEmail)
	if err != nil {
		t.Fatalf("rewrite errored: %v", err)
	}
	if res.Cells != 0 {
		t.Errorf("claimed to mask %d cells of a type it cannot read", res.Cells)
	}
	if !bytes.Equal(tokens(t, out), meta.Bytes()) {
		t.Error("the stream was altered despite the codec being unable to parse it")
	}
}

// Masking must not fire when no rule matches.
func TestUnchangedValuesKeepTheirOriginalBytes(t *testing.T) {
	c := &mssql.Codec{}
	stream := append(colMetaNVarChar("name"), rowNVarChar("Ada")...)
	out, res, err := c.Rewrite(wrap(stream, 0), redactEmail)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cells != 0 || res.Rows != 0 {
		t.Errorf("res = %+v, want nothing rewritten", res)
	}
	if !bytes.Equal(tokens(t, out), stream) {
		t.Error("an untouched result set came back different")
	}
}

// colMetaNoChange is COLMETADATA declaring count 0xFFFF: "no metadata
// change", the server saying the previous layout still describes the rows
// that follow. SQL Server emits it for later result sets of a batch whose
// shape did not move.
func colMetaNoChange() []byte {
	return []byte{0x81, 0xff, 0xff}
}

// The sentinel is not a decoding failure, and treating it as one used to
// latch noRewrite and drop masking for the rest of the CONNECTION. A second
// result set then reached the client in the clear, which is the failure this
// whole path exists to prevent.
func TestNoMetadataChangeKeepsMasking(t *testing.T) {
	c := &mssql.Codec{}

	stream := append(colMetaNVarChar("name", "email"), rowNVarChar("Ada", "ada@example.com")...)
	stream = append(stream, colMetaNoChange()...)
	stream = append(stream, rowNVarChar("Grace", "grace@example.com")...)
	stream = append(stream, doneToken()...)

	out, res, err := c.Rewrite(wrap(stream, 0), redactEmail)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.Rows != 2 || res.Cells != 2 {
		t.Fatalf("res = %+v, want 2 cells in 2 rows", res)
	}

	got := tokens(t, out)
	for _, leaked := range []string{"ada@example.com", "grace@example.com"} {
		if bytes.Contains(got, ucs2(leaked)) {
			t.Errorf("%s reached the client unmasked", leaked)
		}
	}
	if n := bytes.Count(got, ucs2("[REDACTED]")); n != 2 {
		t.Errorf("masked %d cells, want 2", n)
	}
	if !bytes.Contains(got, colMetaNoChange()) {
		t.Error("the no-change token was not forwarded")
	}
	if !bytes.Contains(got, ucs2("Grace")) {
		t.Error("an unmasked column was lost after the no-change token")
	}
}

// The distinction the fix turns on: 0xFFFF keeps the layout, while a real
// count REPLACES it, zero included. Measuring a row against a layout it no
// longer matches consumes the wrong number of bytes and strands everything
// after it in the reassembly buffer.
func TestRealColumnCountReplacesTheLayout(t *testing.T) {
	c := &mssql.Codec{}

	stream := append(colMetaNVarChar("email"), rowNVarChar("ada@example.com")...)
	stream = append(stream, colMetaNVarChar()...) // a real count of zero
	stream = append(stream, 0xd1)                 // a row with no cells
	stream = append(stream, doneToken()...)

	out, res, err := c.Rewrite(wrap(stream, 0), redactEmail)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.Rows != 1 || res.Cells != 1 {
		t.Fatalf("res = %+v, want only the first row rewritten", res)
	}

	got := tokens(t, out)
	if bytes.Contains(got, ucs2("ada@example.com")) {
		t.Error("the original value survived")
	}
	// A stale one-column layout would look for a cell inside the empty row,
	// find nothing, and hold that row and the DONE behind it forever.
	if !bytes.Contains(got, doneToken()) {
		t.Error("the stream never completed: the empty row was measured against a stale layout")
	}
}

// A nil masker is the "masking disabled" path and must be a passthrough.
func TestNilMaskerPassesThrough(t *testing.T) {
	c := &mssql.Codec{}
	in := wrap(append(colMetaNVarChar("email"), rowNVarChar("ada@example.com")...), 0)
	out, _, err := c.Rewrite(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Error("a nil masker changed the stream")
	}
}

// The codec must satisfy the gate's optional re-framing capability, or the
// sidecar will keep refusing mask blocks on this protocol.
func TestCodecIsAReframer(t *testing.T) {
	insp, err := hoopinspect.New(hoopinspect.MSSQL)
	if err != nil {
		t.Fatal(err)
	}
	type reframer interface {
		Rewrite([]byte, func(string, []byte) []byte) ([]byte, hoopinspect.ReframeResult, error)
		Flush(func(string, []byte) []byte) []byte
	}
	if _, ok := insp.Codec().(reframer); !ok {
		t.Fatal("codec/mssql does not implement the re-framing interface")
	}
}

func ucs2Len(s string) []byte {
	enc := ucs2(s)
	out := make([]byte, 2, 2+len(enc))
	binary.LittleEndian.PutUint16(out[0:2], uint16(len(enc)))
	return append(out, enc...)
}
