package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	codec "github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo"
)

// The fixture format of abi/ABI.md, read strictly so a typo in an
// expectation key fails the test instead of checking nothing.
type script struct {
	Name    string            `json:"name"`
	Options map[string]string `json:"options,omitempty"`
	Steps   []step            `json:"steps"`
}

type step struct {
	Dir    string  `json:"dir"`
	Hex    string  `json:"hex"`
	Expect *expect `json:"expect,omitempty"`
}

type expect struct {
	Consumed   *int            `json:"consumed,omitempty"`
	Statements json.RawMessage `json:"statements,omitempty"`
	Error      bool            `json:"error,omitempty"`
}

func loadFixtures(t *testing.T) []script {
	t.Helper()
	raw, err := os.ReadFile("fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var scripts []script
	if err := dec.Decode(&scripts); err != nil {
		t.Fatalf("fixtures.json: %v", err)
	}
	return scripts
}

func opened(t *testing.T, options map[string]string) *memcache {
	t.Helper()
	m := &memcache{}
	if err := m.Open(options); err != nil {
		t.Fatal(err)
	}
	return m
}

func direction(t *testing.T, s string) codec.Direction {
	t.Helper()
	switch s {
	case "client":
		return codec.Client
	case "server":
		return codec.Server
	}
	t.Fatalf("dir %q is not client or server", s)
	return ""
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// connection replays steps the way the host does: decode over the
// unconsumed tail of the same direction, prefixed to the new bytes.
type connection struct {
	codec    *memcache
	leftover map[codec.Direction][]byte
}

func (c *connection) decode(dir codec.Direction, data []byte) ([]codec.Statement, int, error) {
	input := append(append([]byte(nil), c.leftover[dir]...), data...)
	d, err := c.codec.Decode(dir, input)
	if err != nil {
		return nil, 0, err
	}
	if d.Consumed < 0 || d.Consumed > len(input) {
		return nil, 0, fmt.Errorf("consumed %d of %d bytes", d.Consumed, len(input))
	}
	c.leftover[dir] = append([]byte(nil), input[d.Consumed:]...)
	for i := range d.Statements {
		// The host fills the direction when the codec leaves it empty.
		if d.Statements[i].Direction == "" {
			d.Statements[i].Direction = dir
		}
	}
	return d.Statements, d.Consumed, nil
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// subset is the host's comparison: every key of an object, the same
// length and matching elements of an array, equality of a scalar.
func subset(want, got any, path string) (string, bool) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path, false
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				return path + "." + k, false
			}
			if p, ok := subset(wv, gv, path+"."+k); !ok {
				return p, false
			}
		}
		return "", true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return path, false
		}
		for i := range w {
			if p, ok := subset(w[i], g[i], fmt.Sprintf("%s[%d]", path, i)); !ok {
				return p, false
			}
		}
		return "", true
	default:
		if !reflect.DeepEqual(want, got) {
			return path, false
		}
		return "", true
	}
}

func checkStep(t *testing.T, e *expect, stmts []codec.Statement, consumed int, err error) {
	t.Helper()
	if e == nil {
		return
	}
	if e.Error {
		if err == nil {
			t.Fatalf("expected a decode error, got %d statement(s) and consumed %d", len(stmts), consumed)
		}
		return
	}
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if e.Consumed != nil && *e.Consumed != consumed {
		t.Fatalf("consumed %d, want %d", consumed, *e.Consumed)
	}
	if e.Statements == nil {
		return
	}
	var want []any
	if err := json.Unmarshal(e.Statements, &want); err != nil {
		t.Fatalf("expect.statements: %v", err)
	}
	if len(want) != len(stmts) {
		t.Fatalf("%d statement(s), want %d: %s", len(stmts), len(want), mustJSON(stmts))
	}
	for i := range want {
		if path, ok := subset(want[i], asJSON(t, stmts[i]), fmt.Sprintf("statements[%d]", i)); !ok {
			t.Fatalf("%s does not match: got %s", path, mustJSON(stmts[i]))
		}
	}
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// TestFixturesReplayedWhole is conformance check 6 on the host: each
// script, step by step, matches its expectations.
func TestFixturesReplayedWhole(t *testing.T) {
	for _, s := range loadFixtures(t) {
		t.Run(s.Name, func(t *testing.T) {
			conn := &connection{codec: opened(t, s.Options), leftover: map[codec.Direction][]byte{}}
			for i, st := range s.Steps {
				stmts, consumed, err := conn.decode(direction(t, st.Dir), unhex(t, st.Hex))
				t.Logf("step %d: %d statement(s), consumed %d, err %v", i+1, len(stmts), consumed, err)
				checkStep(t, st.Expect, stmts, consumed, err)
				if err != nil {
					break
				}
			}
		})
	}
}

// TestFixturesReplayedByteAtATime is conformance check 7 on the host:
// one byte at a time through the reassembly loop, each step must yield
// the statements the whole replay yields, and fail where it fails.
func TestFixturesReplayedByteAtATime(t *testing.T) {
	for _, s := range loadFixtures(t) {
		t.Run(s.Name, func(t *testing.T) {
			whole := &connection{codec: opened(t, s.Options), leftover: map[codec.Direction][]byte{}}
			bytewise := &connection{codec: opened(t, s.Options), leftover: map[codec.Direction][]byte{}}
			for i, st := range s.Steps {
				dir, data := direction(t, st.Dir), unhex(t, st.Hex)
				wantStmts, _, wantErr := whole.decode(dir, data)
				var gotStmts []codec.Statement
				var gotErr error
				for _, b := range data {
					stmts, _, err := bytewise.decode(dir, []byte{b})
					gotStmts = append(gotStmts, stmts...)
					if err != nil {
						gotErr = err
						break
					}
				}
				if (wantErr != nil) != (gotErr != nil) {
					t.Fatalf("step %d: whole replay error %v, byte-at-a-time error %v", i+1, wantErr, gotErr)
				}
				if wantErr != nil {
					break
				}
				if !reflect.DeepEqual(asJSON(t, wantStmts), asJSON(t, gotStmts)) {
					t.Fatalf("step %d: whole replay %s, byte-at-a-time %s", i+1, mustJSON(wantStmts), mustJSON(gotStmts))
				}
			}
		})
	}
}

func TestLineLimitAppliesAtTheTerminatorNotTheChunk(t *testing.T) {
	m := opened(t, map[string]string{"max_line_bytes": "9"})
	// Nine bytes before CRLF: the limit, not past it.
	d, err := m.Decode(codec.Client, []byte("get abcde\r\n"))
	if err != nil || d.Consumed != 11 {
		t.Fatalf("limit-length line: %+v, %v", d, err)
	}
	// Ten bytes without a terminator cannot become a line of nine.
	if _, err := m.Decode(codec.Client, []byte("get abcdef")); err == nil {
		t.Fatal("a line past the limit without CRLF must be a decode error")
	}
	// Ten bytes ending in CR may still be a nine-byte line plus CRLF.
	d, err = m.Decode(codec.Client, []byte("get abcde\r"))
	if err != nil || d.Consumed != 0 {
		t.Fatalf("line with a pending LF: %+v, %v", d, err)
	}
	// A data block behind the line is not measured against the limit:
	// "set k 0 0 64" is 12 bytes, the payload 64.
	payload := strings.Repeat("x", 64)
	m = opened(t, map[string]string{"max_line_bytes": "12"})
	d, err = m.Decode(codec.Client, []byte("set k 0 0 64\r\n"+payload+"\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Consumed != 14+64+2 || d.Statements[0].Operation != codec.OpUpdate {
		t.Fatalf("%+v", d)
	}
	// One byte less of limit and the command line itself is refused.
	m = opened(t, map[string]string{"max_line_bytes": "11"})
	if _, err := m.Decode(codec.Client, []byte("set k 0 0 64\r\n"+payload+"\r\n")); err == nil {
		t.Fatal("expected the command line to exceed the limit")
	}
}

func TestOpenRefusesABadLineLimit(t *testing.T) {
	for _, raw := range []string{"", "x", "0", "-5"} {
		if err := (&memcache{}).Open(map[string]string{"max_line_bytes": raw}); err == nil {
			t.Errorf("max_line_bytes=%q: expected a refusal", raw)
		}
	}
	m := opened(t, nil)
	if m.maxLineBytes != defaultMaxLineBytes {
		t.Fatalf("default max_line_bytes %d, want %d", m.maxLineBytes, defaultMaxLineBytes)
	}
}

func TestNoreplyIsAKeyForCommandsThatDoNotAcceptIt(t *testing.T) {
	d, err := opened(t, nil).Decode(codec.Client, []byte("get noreply\r\ndelete noreply\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	get, del := d.Statements[0], d.Statements[1]
	if get.Metadata[keyKey] != "noreply" || get.Metadata[noreplyKey] != "" {
		t.Fatalf("get: %+v", get)
	}
	if del.Metadata[keyKey] != "noreply" || del.Metadata[noreplyKey] != "" {
		t.Fatalf("delete: %+v", del)
	}
}

func TestDenyIsOneServerErrorLine(t *testing.T) {
	m := opened(t, nil)
	got := string(m.Deny(codec.Client, "writes are refused\r\non this lane"))
	if got != "SERVER_ERROR writes are refused  on this lane\r\n" {
		t.Fatalf("%q", got)
	}
	if string(m.Deny(codec.Server, "x")) != "SERVER_ERROR x\r\n" {
		t.Fatal("the frame does not depend on the direction")
	}
}

func TestContentRendersRequestsOnly(t *testing.T) {
	m := opened(t, nil)
	d, err := m.Decode(codec.Client, []byte("set user:42 0 0 5 noreply\r\nhello\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	stmt := d.Statements[0]
	stmt.Direction = codec.Client
	c, ok := m.Content(stmt)
	if !ok {
		t.Fatal("a request renders")
	}
	if c.Text != "Protocol: x-memcache\nCommand: set\nKey: user:42\n\nset user:42 0 0 5 noreply" {
		t.Fatalf("%q", c.Text)
	}
	if c.CacheKey != "set|user:" {
		t.Fatalf("cache key %q", c.CacheKey)
	}
	if _, ok := m.Content(codec.Statement{Direction: codec.Server, Text: "STORED"}); ok {
		t.Fatal("a reply must not render")
	}
	if _, ok := m.Content(codec.Statement{Direction: codec.Client, Text: ""}); ok {
		t.Fatal("an empty line must not render")
	}
}
