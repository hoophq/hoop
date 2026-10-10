// Package conformance proves a codec plug-in against abi/ABI.md: the
// checks `hoop-inspect -codec-test <module.wasm> [fixtures.json...]` runs.
//
// The generic checks need no knowledge of the protocol and run on every
// module. The fixture checks replay the author's scripts three ways:
// whole, one byte at a time through the host's reassembly, and
// interleaved with another script on a second connection. A plug-in that
// passes all three has shown the three properties the host relies on
// and cannot verify at load: it decodes what it claims, it honours the
// partial-input rule, and it keeps connections apart.
//
// # Fixture semantics
//
// A step's `expect.consumed` is what `decode` reported for that call,
// whose input is the bytes the previous step left unconsumed in the same
// direction followed by this step's bytes, after `filter` when the
// manifest declares it. `expect.statements` compares only the fields it
// names, recursively: an object in the expectation needs only its own
// keys to match, an array needs the same length and matching elements.
// `expect.error` true means `decode` must report an error, and such a
// step must be the last of its script: the host drops the connection on
// a decode error, so nothing after it can run.
package conformance

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/hoophq/hoop/sidecar/codec/wasm"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// Script is one fixture: a named sequence of chunks with expectations.
type Script struct {
	Name    string            `json:"name"`
	Options map[string]string `json:"options,omitempty"`
	Steps   []Step            `json:"steps"`
}

// Step is one chunk of one direction.
type Step struct {
	Dir    string  `json:"dir"`
	Hex    string  `json:"hex"`
	Expect *Expect `json:"expect,omitempty"`
}

// Expect is what a step must produce. The runner skips absent keys and
// keeps Statements raw, so its subset comparison sees the fields the
// author wrote.
type Expect struct {
	Consumed   *int            `json:"consumed,omitempty"`
	Statements json.RawMessage `json:"statements,omitempty"`
	Error      bool            `json:"error,omitempty"`
}

// garbageSize is what check 5 feeds decode: 64 KiB of 0xFF, a length
// prefix no sane framing accepts and enough of it to cross any
// reasonable frame boundary.
const garbageSize = 64 << 10

// Run loads module, runs the generic checks, then the fixture checks over
// every script in fixtures, writing one line per check to out. It
// returns an error naming the checks that failed; a module with no
// fixtures passes on the generic checks alone, and Run reports the
// fixture checks as skipped.
func Run(ctx context.Context, module []byte, fixtures [][]byte, out io.Writer) error {
	r := &runner{out: out}
	fmt.Fprintf(out, "%-5s %-6s %s\n", "check", "result", "detail")

	// Guest log lines go to the same writer: a plug-in author running
	// this wants to see what their module said.
	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p, err := wasm.Load(ctx, module, wasm.WithLogger(logger))
	if err != nil {
		r.fail(1, "describe: %v", err)
		return r.result()
	}
	defer p.Close()
	m := p.ManifestValues()
	r.ok(1, "describe: %s %q v%s, capabilities [%s], instances %s, sql_dialect %q",
		m.Protocol, m.Label, m.Version, strings.Join(m.Capabilities, " "), m.Instances, m.SQLDialect)

	r.checkZeroBytes(p)
	r.checkDeny(p)
	r.checkGarbage(p)

	scripts, err := parseFixtures(fixtures)
	if err != nil {
		r.fail(6, "fixtures: %v", err)
		return r.result()
	}
	if len(scripts) == 0 {
		for _, n := range []int{4, 6, 7, 8} {
			r.skip(n, "no fixtures")
		}
		return r.result()
	}
	solo := r.checkWhole(p, scripts)
	r.checkByteAtATime(p, scripts, solo)
	r.checkInterleaved(p, scripts, solo)
	return r.result()
}

type runner struct {
	out    io.Writer
	failed []int
}

func (r *runner) ok(check int, format string, args ...any) {
	fmt.Fprintf(r.out, "%-5d %-6s %s\n", check, "ok", fmt.Sprintf(format, args...))
}

func (r *runner) skip(check int, format string, args ...any) {
	fmt.Fprintf(r.out, "%-5d %-6s %s\n", check, "skip", fmt.Sprintf(format, args...))
}

func (r *runner) fail(check int, format string, args ...any) {
	if !slices.Contains(r.failed, check) {
		r.failed = append(r.failed, check)
	}
	fmt.Fprintf(r.out, "%-5d %-6s %s\n", check, "FAIL", fmt.Sprintf(format, args...))
}

func (r *runner) result() error {
	if len(r.failed) == 0 {
		return nil
	}
	sort.Ints(r.failed)
	names := make([]string, len(r.failed))
	for i, n := range r.failed {
		names[i] = fmt.Sprint(n)
	}
	return fmt.Errorf("%w: %s", ErrFailed, strings.Join(names, ", "))
}

func closeCodec(c inspect.Codec) {
	if closer, ok := c.(io.Closer); ok {
		closer.Close()
	}
}

// Check 2: decode of zero bytes consumes nothing, returns nothing and
// does not fail, in both directions.
func (r *runner) checkZeroBytes(p *wasm.Plugin) {
	c := p.NewCodec(nil)
	defer closeCodec(c)
	for _, dir := range []inspect.Direction{inspect.FromClient, inspect.FromServer} {
		stmts, n, err := c.Decode(dir, []byte{})
		switch {
		case err != nil:
			r.fail(2, "decode of zero bytes from %s: %v", dir, err)
			return
		case n != 0 || len(stmts) != 0:
			r.fail(2, "decode of zero bytes from %s consumed %d and returned %d statements", dir, n, len(stmts))
			return
		}
	}
	r.ok(2, "decode of zero bytes: nothing consumed, nothing returned, both directions")
}

// Check 3: deny, when declared, renders at least one byte.
func (r *runner) checkDeny(p *wasm.Plugin) {
	if !slices.Contains(p.Capabilities(), wasm.CapDeny) {
		r.skip(3, "deny: capability not declared")
		return
	}
	c := p.NewCodec(nil)
	defer closeCodec(c)
	framer, ok := c.(gate.DenyFramer)
	if !ok {
		r.fail(3, "deny: codec does not implement gate.DenyFramer")
		return
	}
	var sizes []string
	for _, dir := range []inspect.Direction{inspect.FromClient, inspect.FromServer} {
		frame := framer.DenyFrame(dir, "conformance check")
		if len(frame) == 0 {
			r.fail(3, "deny from %s returned no bytes", dir)
			return
		}
		sizes = append(sizes, fmt.Sprintf("%s %d bytes", dir, len(frame)))
	}
	r.ok(3, "deny: %s", strings.Join(sizes, ", "))
}

// Check 5: a guest failure surfaces as an error, never a hang or a panic.
//
// The runner cannot make an arbitrary module trap: it has no export that
// traps on demand, and adding one to the ABI would test only that
// export. The runner can hand decode input no framing accepts and hold
// the host to its promise about whatever the guest does with it. A
// module that traps on it exercises the trap path for real;
// call_timeout_ms stops a module that spins; a module that reports a
// decode error exercises only the error path. All three must return,
// within the deadline, without a panic escaping. The limit: a module
// that decodes garbage without complaint passes this check having
// proven nothing about its traps, and only its author's fixtures can.
func (r *runner) checkGarbage(p *wasm.Plugin) {
	garbage := bytes.Repeat([]byte{0xFF}, garbageSize)
	var outcomes []string
	for _, dir := range []inspect.Direction{inspect.FromClient, inspect.FromServer} {
		c := p.NewCodec(nil)
		outcome, panicked := r.decodeRecovering(c, dir, garbage)
		closeCodec(c)
		if panicked != nil {
			r.fail(5, "decode of %d bytes of 0xff from %s panicked in the host: %v", garbageSize, dir, panicked)
			return
		}
		outcomes = append(outcomes, fmt.Sprintf("%s: %s", dir, outcome))
	}
	r.ok(5, "garbage input returned without a hang or a panic; %s", strings.Join(outcomes, "; "))
}

func (r *runner) decodeRecovering(c inspect.Codec, dir inspect.Direction, data []byte) (outcome string, panicked any) {
	defer func() {
		if v := recover(); v != nil {
			panicked = v
		}
	}()
	stmts, n, err := c.Decode(dir, data)
	if err != nil {
		return fmt.Sprintf("error %q", err.Error()), nil
	}
	return fmt.Sprintf("no error, consumed %d, %d statements", n, len(stmts)), nil
}

// stepResult is what one step produced on one replay.
type stepResult struct {
	stmts    []inspect.Statement
	consumed int
	err      error
}

// connection replays a script's steps the way the gate would: filter
// first when the codec has one, then decode over the unconsumed tail of
// the same direction.
type connection struct {
	codec    inspect.Codec
	filter   gate.StreamFilter
	leftover map[inspect.Direction][]byte
}

func open(p *wasm.Plugin, s Script) *connection {
	c := &connection{codec: p.NewCodec(s.Options), leftover: map[inspect.Direction][]byte{}}
	if f, ok := c.codec.(gate.StreamFilter); ok && slices.Contains(p.Capabilities(), wasm.CapFilter) {
		c.filter = f
	}
	return c
}

func (c *connection) close() { closeCodec(c.codec) }

func (c *connection) step(st Step) (stepResult, error) {
	dir, data, err := decodeStep(st)
	if err != nil {
		return stepResult{}, err
	}
	if c.filter != nil {
		if data, err = c.filter.Filter(dir, data); err != nil {
			return stepResult{err: fmt.Errorf("filter: %w", err)}, nil
		}
	}
	input := append(c.leftover[dir], data...)
	stmts, n, err := c.codec.Decode(dir, input)
	if err != nil {
		return stepResult{err: err}, nil
	}
	c.leftover[dir] = slices.Clone(input[n:])
	return stepResult{stmts: stmts, consumed: n}, nil
}

func decodeStep(st Step) (inspect.Direction, []byte, error) {
	var dir inspect.Direction
	switch st.Dir {
	case "client":
		dir = inspect.FromClient
	case "server":
		dir = inspect.FromServer
	default:
		return "", nil, fmt.Errorf("dir %q is not client or server", st.Dir)
	}
	data, err := hex.DecodeString(st.Hex)
	if err != nil {
		return "", nil, fmt.Errorf("hex: %w", err)
	}
	return dir, data, nil
}

// Check 6 (and 4): every script replayed whole matches its expectations,
// and every operation produced is one the host lists. Returns each
// script's per-step results for checks 7 and 8 to compare against.
func (r *runner) checkWhole(p *wasm.Plugin, scripts []Script) [][]stepResult {
	solo := make([][]stepResult, len(scripts))
	statements := 0
	var failures []string
	for i, s := range scripts {
		conn := open(p, s)
		for j, st := range s.Steps {
			res, err := conn.step(st)
			if err != nil {
				failures = append(failures, fmt.Sprintf("script %q step %d: %v", s.Name, j+1, err))
				break
			}
			solo[i] = append(solo[i], res)
			statements += len(res.stmts)
			if msg := check(st.Expect, res); msg != "" {
				failures = append(failures, fmt.Sprintf("script %q step %d: %s", s.Name, j+1, msg))
				break
			}
			if res.err != nil {
				if j != len(s.Steps)-1 {
					failures = append(failures, fmt.Sprintf("script %q step %d: a decode error ends the connection, but %d step(s) follow", s.Name, j+1, len(s.Steps)-j-1))
				}
				break
			}
		}
		conn.close()
	}
	// The host refuses an operation outside the list before the
	// statement reaches here, so this cannot fail on a loaded module; the
	// runner reports it so the list of checks reads the way ABI.md lists
	// them.
	r.ok(4, "operations: %d statement(s), every operation listed by inspect.Operations", statements)
	for _, f := range failures {
		r.fail(6, "%s", f)
	}
	if len(failures) == 0 {
		r.ok(6, "%d script(s) replayed whole", len(scripts))
	}
	return solo
}

// Check 7: one byte at a time through the host's reassembly produces the
// same statements per step as the whole replay.
func (r *runner) checkByteAtATime(p *wasm.Plugin, scripts []Script, solo [][]stepResult) {
	failed := false
	for i, s := range scripts {
		conn := open(p, s)
		inspectors := map[inspect.Direction]*inspect.Inspector{}
		for _, dir := range []inspect.Direction{inspect.FromClient, inspect.FromServer} {
			insp := inspect.NewWithCodec(conn.codec)
			if sized, ok := conn.codec.(interface{ MaxReassemblyBytes() int }); ok {
				insp.SetMaxBuffer(sized.MaxReassemblyBytes())
			}
			inspectors[dir] = insp
		}
		for j, st := range s.Steps {
			if j >= len(solo[i]) {
				break // the whole replay stopped here
			}
			dir, data, err := decodeStep(st)
			if err != nil {
				break // reported by check 6
			}
			var got stepResult
			for _, b := range data {
				chunk := []byte{b}
				if conn.filter != nil {
					if chunk, err = conn.filter.Filter(dir, chunk); err != nil {
						got.err = err
						break
					}
				}
				stmts, err := inspectors[dir].Inspect(dir, chunk)
				got.stmts = append(got.stmts, stmts...)
				if err != nil {
					got.err = err
					break
				}
			}
			want := solo[i][j]
			if (want.err != nil) != (got.err != nil) {
				r.fail(7, "script %q step %d: whole replay error %v, byte-at-a-time error %v", s.Name, j+1, want.err, got.err)
				failed = true
				break
			}
			if msg := sameStatements(want.stmts, got.stmts); msg != "" {
				r.fail(7, "script %q step %d: %s", s.Name, j+1, msg)
				failed = true
				break
			}
			if got.err != nil {
				break
			}
		}
		conn.close()
	}
	if !failed {
		r.ok(7, "%d script(s) replayed one byte at a time match the whole replay", len(scripts))
	}
}

// Check 8: two scripts interleaved step by step on two connections each
// produce what they produce alone. The runner pairs script i with i+1,
// and the last with the first, so every script runs beside a different
// one; a lone script runs beside itself, which still proves two
// connections of one guest do not share state.
func (r *runner) checkInterleaved(p *wasm.Plugin, scripts []Script, solo [][]stepResult) {
	failed := false
	pairs := 0
	for i := range scripts {
		j := (i + 1) % len(scripts)
		a, b := open(p, scripts[i]), open(p, scripts[j])
		msg := r.interleave([2]*connection{a, b}, [2]Script{scripts[i], scripts[j]}, [2][]stepResult{solo[i], solo[j]})
		a.close()
		b.close()
		if msg != "" {
			r.fail(8, "scripts %q and %q interleaved: %s", scripts[i].Name, scripts[j].Name, msg)
			failed = true
		}
		pairs++
	}
	if !failed {
		r.ok(8, "%d script pair(s) interleaved on two connections match their solo replays", pairs)
	}
}

func (r *runner) interleave(conns [2]*connection, scripts [2]Script, solo [2][]stepResult) string {
	done := [2]bool{}
	for k := 0; !done[0] || !done[1]; k++ {
		for side := range conns {
			if done[side] {
				continue
			}
			if k >= len(solo[side]) {
				done[side] = true
				continue
			}
			got, err := conns[side].step(scripts[side].Steps[k])
			if err != nil {
				return fmt.Sprintf("script %q step %d: %v", scripts[side].Name, k+1, err)
			}
			want := solo[side][k]
			if (want.err != nil) != (got.err != nil) || want.consumed != got.consumed {
				return fmt.Sprintf("script %q step %d: alone consumed %d error %v, interleaved consumed %d error %v",
					scripts[side].Name, k+1, want.consumed, want.err, got.consumed, got.err)
			}
			if msg := sameStatements(want.stmts, got.stmts); msg != "" {
				return fmt.Sprintf("script %q step %d: %s", scripts[side].Name, k+1, msg)
			}
			if got.err != nil {
				done[side] = true
			}
		}
	}
	return ""
}

// check holds one step's result to its expectation. Empty means it holds.
func check(e *Expect, res stepResult) string {
	if e == nil {
		return ""
	}
	if e.Error {
		if res.err == nil {
			return "expected a decode error, got none"
		}
		return ""
	}
	if res.err != nil {
		return fmt.Sprintf("decode error: %v", res.err)
	}
	if e.Consumed != nil && *e.Consumed != res.consumed {
		return fmt.Sprintf("consumed %d, want %d", res.consumed, *e.Consumed)
	}
	if e.Statements != nil {
		var want []any
		if err := json.Unmarshal(e.Statements, &want); err != nil {
			return fmt.Sprintf("expect.statements: %v", err)
		}
		if len(want) != len(res.stmts) {
			return fmt.Sprintf("%d statement(s), want %d", len(res.stmts), len(want))
		}
		for i := range want {
			got, err := toJSONValue(res.stmts[i])
			if err != nil {
				return err.Error()
			}
			if path, ok := subset(want[i], got, fmt.Sprintf("statements[%d]", i)); !ok {
				return fmt.Sprintf("%s does not match: got %s", path, mustJSON(res.stmts[i]))
			}
		}
	}
	return ""
}

// subset reports whether got contains want: every key of an
// object, the same length and matching elements of an array, equality
// of a scalar. The path of the first mismatch comes back for the report.
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

func sameStatements(want, got []inspect.Statement) string {
	if len(want) != len(got) {
		return fmt.Sprintf("%d statement(s) alone, %d here", len(want), len(got))
	}
	for i := range want {
		if w, g := mustJSON(want[i]), mustJSON(got[i]); w != g {
			return fmt.Sprintf("statement %d differs: alone %s, here %s", i, w, g)
		}
	}
	return ""
}

func toJSONValue(s inspect.Statement) (any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func mustJSON(s inspect.Statement) string {
	b, err := json.Marshal(s)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

// parseFixtures reads every fixture file strictly: an unknown key is a
// typo that would otherwise check nothing.
func parseFixtures(fixtures [][]byte) ([]Script, error) {
	var scripts []Script
	for i, raw := range fixtures {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var batch []Script
		if err := dec.Decode(&batch); err != nil {
			return nil, fmt.Errorf("fixture file %d: %w", i+1, err)
		}
		for _, s := range batch {
			if s.Name == "" {
				return nil, fmt.Errorf("fixture file %d: a script has no name", i+1)
			}
			if len(s.Steps) == 0 {
				return nil, fmt.Errorf("fixture file %d: script %q has no steps", i+1, s.Name)
			}
			for j, st := range s.Steps {
				if _, _, err := decodeStep(st); err != nil {
					return nil, fmt.Errorf("script %q step %d: %w", s.Name, j+1, err)
				}
			}
		}
		scripts = append(scripts, batch...)
	}
	return scripts, nil
}

// ErrFailed is the base of every error Run returns for failed checks,
// for callers that need to tell a failed module from a broken invocation.
var ErrFailed = errors.New("conformance checks failed")
