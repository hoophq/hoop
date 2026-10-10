package codec

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// echo yields one statement per byte, so dispatch tests can tell
// connections and calls apart.
type echo struct {
	prefix string
}

func (e *echo) Open(options map[string]string) error {
	e.prefix = options["prefix"]
	if e.prefix == "refuse" {
		return errors.New("refused")
	}
	return nil
}

func (e *echo) Decode(dir Direction, data []byte) (Decoded, error) {
	if len(data) > 0 && data[0] == '!' {
		return Decoded{}, errors.New("bang")
	}
	var d Decoded
	for _, b := range data {
		d.Statements = append(d.Statements, Statement{Operation: OpOther, Text: e.prefix + string(b)})
	}
	d.Consumed = len(data)
	return d, nil
}

func (e *echo) Deny(dir Direction, message string) []byte { return []byte(e.prefix + ":" + message) }

func (e *echo) TakeCredential(stmt Statement) (string, Statement, bool) {
	token, ok := stmt.Metadata["token"]
	if !ok {
		return "", stmt, false
	}
	delete(stmt.Metadata, "token")
	return token, stmt, true
}

func (e *echo) Content(stmt Statement) (Content, bool) {
	if stmt.Text == "" {
		return Content{}, false
	}
	return Content{Text: stmt.Text, CacheKey: "k"}, true
}

func newEcho() Codec { return &echo{} }

func parse(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not JSON: %s: %v", raw, err)
	}
	return v
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: expected a panic", name)
		}
	}()
	f()
}

func TestPackedReturnRoundTrips(t *testing.T) {
	if got := pack(0x1000, 7); got != 0x0000_1000_0000_0007 {
		t.Fatalf("pack = %#x", got)
	}
	ptr, n := unpack(pack(^uint32(0), 5))
	if ptr != ^uint32(0) || n != 5 {
		t.Fatalf("unpack = %#x, %d", ptr, n)
	}
	if give(nil) != 0 {
		t.Fatal("empty output must be the packed 0")
	}
}

func TestStatementJSONUsesTheABINamesAndOmitsEmptyFields(t *testing.T) {
	s := Statement{
		Operation: OpDelete, Text: "PURGE orders",
		Effects: []Operation{OpDelete}, Relations: []Relation{{Name: "orders", Access: AccessWrite}}, Tables: []string{"orders"},
		Result: &ResultDetail{Columns: []Column{{Name: "id"}}, RowCount: 3},
	}.WithMetadata("x-acmewire.verb", "PURGE")
	raw, _ := json.Marshal(s)
	want := `{"text":"PURGE orders","operation":"delete","effects":["delete"],"relations":[{"name":"orders","access":"write"}],"tables":["orders"],"result":{"columns":[{"name":"id"}],"row_count":3},"metadata":{"x-acmewire.verb":"PURGE"}}`
	if string(raw) != want {
		t.Fatalf("got  %s\nwant %s", raw, want)
	}
	raw, _ = json.Marshal(Statement{Operation: OpOther, Text: "AUTH"})
	if string(raw) != `{"text":"AUTH","operation":"other"}` {
		t.Fatalf("minimum statement: %s", raw)
	}

	// What the host hands back: its own Statement, direction set, maybe
	// with protocol, which the guest ignores and never emits.
	var in Statement
	if err := json.Unmarshal([]byte(`{"protocol":"x","direction":"server","text":"ROW","operation":"other","result":{"row_count":1,"truncated":true}}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Direction != Server || !in.Result.Truncated {
		t.Fatalf("parsed %+v", in)
	}
}

func TestWithAnalysisFailsClosedOnIncompleteScan(t *testing.T) {
	var a SQLAnalysis
	json.Unmarshal([]byte(`{"operation":"select","effects":["select"],"relations":[{"name":"t","access":"read"}],"tables":["t"],"complete":true,"reason":""}`), &a)
	s := Statement{Text: "SELECT 1 FROM t"}.WithAnalysis(a)
	if s.Operation != OpSelect || s.Tables[0] != "t" || len(s.Metadata) != 0 {
		t.Fatalf("%+v", s)
	}
	json.Unmarshal([]byte(`{"operation":"unknown","complete":false,"reason":"unterminated string"}`), &a)
	s = Statement{Text: "SELECT '", Operation: OpSelect}.WithAnalysis(a)
	if s.Operation != OpUnknown || s.Metadata[MetadataSQLIncomplete] != "unterminated string" {
		t.Fatalf("%+v", s)
	}
}

func TestManifestJSONMatchesTheABIExample(t *testing.T) {
	r := newRegistry(newEcho, Manifest{
		Protocol: "x-acmewire", Label: "Acme Wire", Version: "1.3.0",
		Capabilities: []Capability{CapDeny, CapContent}, SQLDialect: "mysql", MaxReassembly: 8388608,
		Instances: "per_connection", CallTimeoutMS: 2000, MemoryLimitPages: 1024,
		Options: []OptionSpec{{Name: "compat_mode", Label: "Compat mode", Type: "enum", Values: []string{"v3", "v4"}, Default: "v4", Help: "..."}},
	})
	declared = []Capability{CapContent, CapDeny}
	defer func() { declared = nil }()
	want := `{"abi":1,"protocol":"x-acmewire","label":"Acme Wire","version":"1.3.0","capabilities":["deny","content"],"sql_dialect":"mysql","max_reassembly":8388608,"instances":"per_connection","call_timeout_ms":2000,"memory_limit_pages":1024,"options":[{"name":"compat_mode","label":"Compat mode","type":"enum","values":["v3","v4"],"default":"v4","help":"..."}]}`
	if got := string(r.describe()); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	raw, _ := json.Marshal(newRegistry(newEcho, Manifest{Protocol: "x-min", Label: "Min"}).manifest)
	if string(raw) != `{"abi":1,"protocol":"x-min","label":"Min"}` {
		t.Fatalf("minimum manifest: %s", raw)
	}
}

func TestDescribeHoldsManifestToBuildTagsAndInterfaces(t *testing.T) {
	defer func() { declared = nil }()
	declared = []Capability{CapDeny}
	mustPanic(t, "capability without its export", func() {
		newRegistry(newEcho, Manifest{Protocol: "x-t", Label: "T", Capabilities: []Capability{CapDeny, CapFilter}}).describe()
	})
	mustPanic(t, "export without its capability", func() {
		newRegistry(newEcho, Manifest{Protocol: "x-t", Label: "T"}).describe()
	})
	declared = []Capability{CapFilter}
	mustPanic(t, "declared capability the codec does not implement", func() {
		newRegistry(newEcho, Manifest{Protocol: "x-t", Label: "T", Capabilities: []Capability{CapFilter}}).describe()
	})
}

func TestRegistryDispatchesByConnAndKeepsStateApart(t *testing.T) {
	r := newRegistry(newEcho, Manifest{Protocol: "x-t", Label: "T"})
	if rc := r.open(1, []byte(`{"prefix":"a"}`)); rc != 0 {
		t.Fatalf("open 1: %d", rc)
	}
	if rc := r.open(2, nil); rc != 0 {
		t.Fatalf("open 2: %d", rc)
	}
	if rc := r.open(3, []byte(`{"prefix":"refuse"}`)); rc != 2 {
		t.Fatalf("refused open: %d", rc)
	}
	if rc := r.open(4, []byte(`[]`)); rc != 1 {
		t.Fatalf("options must be a string map: %d", rc)
	}
	one := parse(t, r.decode(1, 0, []byte("xy")))
	stmts := one["statements"].([]any)
	if one["consumed"] != 2.0 || stmts[0].(map[string]any)["text"] != "ax" || stmts[1].(map[string]any)["text"] != "ay" {
		t.Fatalf("conn 1: %v", one)
	}
	two := parse(t, r.decode(2, 1, []byte("z")))
	if two["statements"].([]any)[0].(map[string]any)["text"] != "z" {
		t.Fatalf("conn 2: %v", two)
	}
	if got := string(r.decode(2, 0, nil)); got != `{"statements":[],"consumed":0}` {
		t.Fatalf("empty decode: %s", got)
	}
	if got := parse(t, r.decode(1, 0, []byte("!"))); got["error"] != "bang" || got["statements"] != nil {
		t.Fatalf("error result: %v", got)
	}
	if got := parse(t, r.decode(9, 0, []byte("x"))); !strings.Contains(got["error"].(string), "not opened") {
		t.Fatalf("unopened: %v", got)
	}
	if got := string(r.deny(1, 0, []byte("no"))); got != "a:no" {
		t.Fatalf("deny: %s", got)
	}
	r.close(1)
	mustPanic(t, "deny on a closed connection", func() { r.deny(1, 0, []byte("no")) })
	r.close(1)
}

func TestCapabilityExportsRenderTheirResultJSON(t *testing.T) {
	r := newRegistry(newEcho, Manifest{Protocol: "x-t", Label: "T"})
	r.open(0, []byte(`{}`))
	lifted := parse(t, r.takeCredential(0, []byte(`{"direction":"client","text":"AUTH","operation":"other","metadata":{"token":"s3","keep":"1"}}`)))
	if lifted["ok"] != true || lifted["credential"] != "s3" {
		t.Fatalf("lifted: %v", lifted)
	}
	if md := lifted["statement"].(map[string]any)["metadata"]; !reflect.DeepEqual(md, map[string]any{"keep": "1"}) {
		t.Fatalf("scrubbed metadata: %v", md)
	}
	if got := string(r.takeCredential(0, []byte(`{"text":"x","operation":"other"}`))); got != `{"ok":false}` {
		t.Fatalf("no credential: %s", got)
	}
	if got := string(r.content(0, []byte(`{"text":"SELECT 1","operation":"select"}`))); got != `{"ok":true,"text":"SELECT 1","cache_key":"k"}` {
		t.Fatalf("content: %s", got)
	}
	if got := string(r.content(0, []byte(`{"text":"","operation":"other"}`))); got != `{"ok":false}` {
		t.Fatalf("no content: %s", got)
	}
	if got := rewriteJSON(Rewritten{Bytes: []byte("abc"), Cells: 2, Rows: 1}, nil); string(got) != `{"bytes":"YWJj","cells":2,"rows":1}` {
		t.Fatalf("rewrite: %s", got)
	}
	if got := rewriteJSON(Rewritten{}, errors.New("broken")); string(got) != `{"error":"broken"}` {
		t.Fatalf("rewrite error: %s", got)
	}
}

// The exports move bytes through alloc/borrow/give; on the host the
// addresses are truncated to 32 bits but used consistently, so the path
// runs without a wasm runtime.
func TestExportsMoveBytesThroughLinearMemory(t *testing.T) {
	served = newRegistry(newEcho, Manifest{Protocol: "x-t", Label: "T"})
	defer func() { served = nil }()

	put := func(b []byte) (uint32, uint32) {
		ptr := alloc(uint32(len(b)))
		copy(live[ptr], b)
		return ptr, uint32(len(b))
	}
	read := func(packed uint64) []byte {
		ptr, n := unpack(packed)
		out := append([]byte(nil), live[ptr][:n]...)
		free(ptr, n)
		return out
	}

	optr, on := put([]byte(`{"prefix":"p"}`))
	if rc := exportOpen(0, optr, on); rc != 0 {
		t.Fatalf("open: %d", rc)
	}
	free(optr, on)
	dptr, dn := put([]byte("q"))
	got := parse(t, read(exportDecode(0, 0, dptr, dn)))
	free(dptr, dn)
	if got["statements"].([]any)[0].(map[string]any)["text"] != "pq" {
		t.Fatalf("decode through memory: %v", got)
	}
	if empty := string(read(exportDecode(0, 0, 0, 0))); empty != `{"statements":[],"consumed":0}` {
		t.Fatalf("decode of nothing: %s", empty)
	}
	exportClose(0)
	if len(live) != 0 {
		t.Fatalf("every region the host freed must leave live; %d remain", len(live))
	}
	mustPanic(t, "a region alloc did not hand out", func() { borrow(0xdead, 4) })
}

func TestDirectionOfRejectsAnythingButZeroAndOne(t *testing.T) {
	if directionOf(0) != Client || directionOf(1) != Server {
		t.Fatal("0 is the client, 1 the server")
	}
	mustPanic(t, "direction 2", func() { directionOf(2) })
}
