package main

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	codec "github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo"
)

func opened(t *testing.T) *acmeWire {
	t.Helper()
	a := &acmeWire{}
	if err := a.Open(nil); err != nil {
		t.Fatal(err)
	}
	return a
}

func rowFrame(cells ...[]byte) []byte {
	var payload []byte
	for _, c := range cells {
		payload = binary.BigEndian.AppendUint32(payload, uint32(len(c)))
		payload = append(payload, c...)
	}
	return encode('D', payload)
}

func TestPurgeIsADeleteOnTheLowercasedTable(t *testing.T) {
	d, err := opened(t).Decode(codec.Client, encode('P', []byte("Orders")))
	if err != nil {
		t.Fatal(err)
	}
	s := d.Statements[0]
	if d.Consumed != 11 || s.Operation != codec.OpDelete || s.Text != "PURGE Orders" || s.Tables[0] != "orders" ||
		s.Relations[0].Access != codec.AccessWrite || s.Metadata[verbKey] != "PURGE" {
		t.Fatalf("%+v", d)
	}
}

func TestPartialFrameWaitsAndBadOpcodeFailsAtOnce(t *testing.T) {
	a := opened(t)
	d, err := a.Decode(codec.Client, encode('P', []byte("orders"))[:8])
	if err != nil || d.Consumed != 0 || len(d.Statements) != 0 {
		t.Fatalf("partial: %+v %v", d, err)
	}
	two := append(encode('P', []byte("orders")), 'A', 0, 0)
	d, err = a.Decode(codec.Client, two)
	if err != nil || d.Consumed != 11 || len(d.Statements) != 1 {
		t.Fatalf("frame plus dangling header: %+v %v", d, err)
	}
	if _, err := a.Decode(codec.Client, []byte("X")); err == nil {
		t.Fatal("the codec judges the opcode before the length arrives")
	}
	if _, err := a.Decode(codec.Client, encode('C', nil)); err == nil {
		t.Fatal("a server opcode from the client")
	}
}

func TestServerFramesReportResultShape(t *testing.T) {
	a := opened(t)
	in := encode('C', []byte("id\x00email"))
	in = append(in, rowFrame([]byte("1"), []byte("a@b"))...)
	in = append(in, encode('R', []byte{0, 0, 0, 3})...)
	in = append(in, encode('E', []byte("boom"))...)
	d, err := a.Decode(codec.Server, in)
	if err != nil || d.Consumed != len(in) || len(d.Statements) != 4 {
		t.Fatalf("%+v %v", d, err)
	}
	if cols := d.Statements[0].Result.Columns; len(cols) != 2 || cols[1].Name != "email" {
		t.Fatalf("columns: %+v", cols)
	}
	if d.Statements[1].Result.RowCount != 1 || d.Statements[2].Result.RowCount != 3 || d.Statements[3].Text != "ERROR: boom" {
		t.Fatalf("%+v", d.Statements)
	}
	if _, err := a.Decode(codec.Server, encode('R', []byte("12"))); err == nil {
		t.Fatal("R without a u32")
	}
	if _, err := a.Decode(codec.Server, encode('D', []byte{0, 0, 0, 9, 'x'})); err == nil {
		t.Fatal("cell overrun")
	}
}

func TestFilterStripsPaddingOnlyBetweenFramesUnderAnyChunking(t *testing.T) {
	whole := []byte{0, 0}
	whole = append(whole, encode('D', []byte{0, 0, 0, 1, 0})...) // a zero cell inside the payload
	whole = append(whole, 0)
	whole = append(whole, encode('R', []byte{0, 0, 0, 1})...)
	want := append(encode('D', []byte{0, 0, 0, 1, 0}), encode('R', []byte{0, 0, 0, 1})...)

	if got := opened(t).Filter(codec.Server, whole); !bytes.Equal(got, want) {
		t.Fatalf("whole: %x", got)
	}
	a := opened(t)
	var bytewise []byte
	for _, b := range whole {
		bytewise = append(bytewise, a.Filter(codec.Server, []byte{b})...)
	}
	if !bytes.Equal(bytewise, want) {
		t.Fatalf("bytewise: %x", bytewise)
	}
}

func TestRewriteMasksCellsByColumnAndRecomputesLengths(t *testing.T) {
	a := opened(t)
	a.EnableRewrite()
	chunk := append(encode('C', []byte("id\x00email")), rowFrame([]byte("1"), []byte("a@b"))...)
	var seen []string
	out, err := a.Rewrite(chunk, func(col string, val []byte) []byte {
		seen = append(seen, col+"="+string(val))
		if col == "email" {
			return []byte("***")
		}
		return val
	})
	if err != nil {
		t.Fatal(err)
	}
	want := append(encode('C', []byte("id\x00email")), rowFrame([]byte("1"), []byte("***"))...)
	if !bytes.Equal(out.Bytes, want) || out.Cells != 1 || out.Rows != 1 || len(seen) != 2 || seen[1] != "email=a@b" {
		t.Fatalf("%+v seen=%v", out, seen)
	}
	if untouched, _ := a.Rewrite(rowFrame([]byte("3"), []byte("e@f")), func(_ string, v []byte) []byte { return v }); untouched.Cells != 0 || untouched.Rows != 0 {
		t.Fatalf("a row mask left alone counts nothing: %+v", untouched)
	}

	row := rowFrame([]byte("2"), []byte("c@d"))
	first, _ := a.Rewrite(row[:7], func(_ string, v []byte) []byte { return v })
	if len(first.Bytes) != 0 {
		t.Fatal("the codec holds a split frame")
	}
	second, _ := a.Rewrite(row[7:], func(string, []byte) []byte { return []byte("x") })
	if !bytes.Equal(second.Bytes, rowFrame([]byte("x"), []byte("x"))) {
		t.Fatalf("rebuilt: %x", second.Bytes)
	}
	if fl, _ := a.Flush(nil); len(fl.Bytes) != 0 {
		t.Fatal("flush holds nothing")
	}
}

func TestDenyCredentialAndContent(t *testing.T) {
	a := &acmeWire{}
	if err := a.Open(map[string]string{"deny_prefix": "NOPE"}); err != nil {
		t.Fatal(err)
	}
	if got := a.Deny(codec.Client, "blocked"); !bytes.Equal(got, encode('E', []byte("NOPE: blocked"))) {
		t.Fatalf("deny: %q", got)
	}
	if err := a.Open(map[string]string{"deny_prefix": ""}); err == nil {
		t.Fatal("an empty prefix is refused")
	}

	a = opened(t)
	d, _ := a.Decode(codec.Client, encode('A', []byte("s3cret")))
	auth := d.Statements[0]
	if !reflect.DeepEqual(auth.Metadata, map[string]string{verbKey: "AUTH", credentialKey: "1"}) {
		t.Fatalf("auth statement carries %v, want the handle only", auth.Metadata)
	}
	token, scrubbed, ok := a.TakeCredential(auth)
	if !ok || token != "s3cret" || !reflect.DeepEqual(scrubbed.Metadata, map[string]string{verbKey: "AUTH"}) {
		t.Fatalf("%q %+v", token, scrubbed)
	}
	// The handle is spent: the same statement lifts nothing twice.
	if _, _, ok := a.TakeCredential(auth); ok {
		t.Fatal("a spent handle lifted a token")
	}
	if _, _, ok := a.TakeCredential(scrubbed); ok {
		t.Fatal("scrubbed statement carries no credential")
	}
	// Handles count up per connection, and another connection does not know them.
	d, _ = a.Decode(codec.Client, encode('A', []byte("other")))
	if d.Statements[0].Metadata[credentialKey] != "2" {
		t.Fatalf("second handle %v", d.Statements[0].Metadata)
	}
	if _, _, ok := opened(t).TakeCredential(d.Statements[0]); ok {
		t.Fatal("another connection resolved the handle")
	}
	if token, _, ok := a.TakeCredential(d.Statements[0]); !ok || token != "other" {
		t.Fatalf("second lift %q %v", token, ok)
	}

	stmt := codec.Statement{Operation: codec.OpDelete, Text: "PURGE orders2024"}.WithMetadata(verbKey, "PURGE")
	c, ok := a.Content(stmt)
	if !ok || c.Text != "Protocol: x-acmewire\nVerb: PURGE\n\nPURGE orders2024" || c.CacheKey != "PURGE|purge orders" {
		t.Fatalf("%+v", c)
	}
	stmt.Direction = codec.Server
	if _, ok := a.Content(stmt); ok {
		t.Fatal("server statements render nothing")
	}
}

func TestDecodeRefusesToHoldUnboundedCredentials(t *testing.T) {
	a := opened(t)
	for i := range maxHeldCredentials {
		if _, err := a.Decode(codec.Client, encode('A', []byte("t"))); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if _, err := a.Decode(codec.Client, encode('A', []byte("t"))); err == nil {
		t.Fatal("one more token than the bound was held")
	}
}
