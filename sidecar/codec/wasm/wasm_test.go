package wasm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/tetratelabs/wazero/api"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v (run testdata/cfixture/build.sh)", err)
	}
	return b
}

func load(t *testing.T, name string, opts ...Option) *Plugin {
	t.Helper()
	p, err := Load(context.Background(), fixture(t, name), opts...)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// frame encodes one x-cfix frame.
func frame(text string) []byte {
	return append([]byte{byte(len(text))}, text...)
}

func TestLoadRefusesBrokenModules(t *testing.T) {
	cases := []struct{ file, want string }{
		{"cfixture_badabi.wasm", "the module declares abi 2"},
		{"cfixture_builtin.wasm", `protocol "postgres" must match`},
		{"cfixture_noprefix.wasm", `protocol "cfix" must match`},
		{"cfixture_capnoexport.wasm", `capability "filter" but the module does not export "filter"`},
		{"cfixture_exportnocap.wasm", `exports "deny" but the manifest does not name capability "deny"`},
		{"cfixture_undeclared.wasm", "imports env.bogus"},
		{"cfixture_nodialect.wasm", "names no sql_dialect"},
		{"cfixture_wasiundeclared.wasm", "does not say wasi"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			p, err := Load(context.Background(), fixture(t, tc.file))
			if err == nil {
				p.Close()
				t.Fatalf("Load accepted %s", tc.file)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name the rule %q", err, tc.want)
			}
		})
	}
	if _, err := Load(context.Background(), []byte("not wasm")); err == nil || !strings.Contains(err.Error(), "compile") {
		t.Fatalf("garbage module: %v", err)
	}
}

func TestLoadRefusesBuiltinProtocolByRegistry(t *testing.T) {
	// The pattern already excludes libhoop's names; a plug-in must not
	// shadow an x- codec a custom binary registered either.
	if _, err := parseManifest([]byte(`{"abi":1,"protocol":"postgres","label":"x"}`)); err == nil {
		t.Fatal("built-in protocol accepted")
	}
}

func TestManifestAndDescribe(t *testing.T) {
	p := load(t, "cfixture.wasm")
	if p.Protocol() != "x-cfix" || p.Label() != "C Fixture" || p.Version() != "0.1.0" {
		t.Fatalf("manifest: %s %q %q", p.Protocol(), p.Label(), p.Version())
	}
	if got := p.Capabilities(); len(got) != 1 || got[0] != CapDeny {
		t.Fatalf("capabilities %v", got)
	}
	m, err := parseManifest(p.Manifest())
	if err != nil {
		t.Fatalf("Manifest() does not parse: %v", err)
	}
	if m.Instances != InstancesPerConnection || m.callTimeoutMS() != DefaultCallTimeoutMS || m.memoryLimitPages() != DefaultMemoryLimitPages {
		t.Fatalf("defaults not applied: %+v", m)
	}
	if len(m.Options) != 1 || m.Options[0].Name != "refuse" || m.Options[0].Type != "bool" {
		t.Fatalf("options %+v", m.Options)
	}
}

func TestDecode(t *testing.T) {
	p := load(t, "cfixture.wasm")
	c := p.NewCodec(nil)
	defer c.(interface{ Close() error }).Close()

	data := append(frame("hello"), frame(`quote " and \ back`)...)
	stmts, n, err := c.Decode(inspect.FromClient, data)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(data) || len(stmts) != 2 {
		t.Fatalf("consumed %d of %d, %d statements", n, len(data), len(stmts))
	}
	s := stmts[1]
	if s.Protocol != "x-cfix" || s.Direction != inspect.FromClient || s.Operation != inspect.OpOther || s.Text != `quote " and \ back` {
		t.Fatalf("statement %+v", s)
	}
	if s.Metadata["x-cfix.verb"] != "TEXT" || s.Metadata["x-cfix.seq"] != "2" || s.Metadata["x-cfix.initialized"] != "1" {
		t.Fatalf("metadata %v", s.Metadata)
	}
	// Server direction defaults onto the statement.
	stmts, _, err = c.Decode(inspect.FromServer, frame("reply"))
	if err != nil || stmts[0].Direction != inspect.FromServer {
		t.Fatalf("server direction: %v %+v", err, stmts)
	}
	// Zero bytes: nothing consumed, nothing decoded, no error.
	if stmts, n, err = c.Decode(inspect.FromClient, nil); err != nil || n != 0 || len(stmts) != 0 {
		t.Fatalf("zero bytes: %v %d %v", err, n, stmts)
	}
	// The guest's own decode error surfaces as an error naming the protocol.
	if _, _, err = c.Decode(inspect.FromClient, []byte{0}); err == nil || err.Error() != "empty frame" || !errors.Is(err, inspect.ErrStreamUnsafe) {
		t.Fatalf("empty frame: %v", err)
	}
	// The instance stays usable after a decode error.
	if _, _, err = c.Decode(inspect.FromClient, frame("still here")); err != nil {
		t.Fatalf("after a decode error: %v", err)
	}
}

func TestPartialInputIsRetained(t *testing.T) {
	p := load(t, "cfixture.wasm")
	c := p.NewCodec(nil)
	insp := inspect.NewWithCodec(c)
	data := append(frame("first"), frame("second")...)
	var got []string
	for i := range data {
		stmts, err := insp.Inspect(inspect.FromClient, data[i:i+1])
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range stmts {
			got = append(got, s.Text)
		}
	}
	if strings.Join(got, ",") != "first,second" || insp.Buffered() != 0 {
		t.Fatalf("got %v, buffered %d", got, insp.Buffered())
	}
}

func TestTrapKillsTheInstance(t *testing.T) {
	p := load(t, "cfixture_trap.wasm")
	c := p.NewCodec(nil)
	_, _, err := c.Decode(inspect.FromClient, frame("x"))
	if err == nil || err.Error() != "x-cfix: decode: wasm error: unreachable" || !errors.Is(err, inspect.ErrStreamUnsafe) {
		t.Fatalf("trap: %v", err)
	}
	// Dead for good: the same cause behind every later call, and the
	// deny frame is gone too.
	_, _, again := c.Decode(inspect.FromClient, frame("x"))
	if !errors.Is(again, errDead) || !strings.HasSuffix(again.Error(), err.Error()) {
		t.Fatalf("second call: %v", again)
	}
	if f := c.(gate.DenyFramer).DenyFrame(inspect.FromClient, "no"); f != nil {
		t.Fatalf("deny on a dead instance returned %q", f)
	}
	// Another connection is unaffected.
	c2 := p.NewCodec(nil)
	if f := c2.(gate.DenyFramer).DenyFrame(inspect.FromClient, "no"); string(f) != "CFIX:no" {
		t.Fatalf("fresh instance deny: %q", f)
	}
}

func TestTimeoutKillsTheInstance(t *testing.T) {
	p := load(t, "cfixture_spin.wasm")
	c := p.NewCodec(nil)
	start := time.Now()
	_, _, err := c.Decode(inspect.FromClient, frame("x"))
	if err == nil || !strings.Contains(err.Error(), "exceeded call_timeout_ms (200ms)") {
		t.Fatalf("timeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s to stop a spinning guest", elapsed)
	}
	if _, _, err := c.Decode(inspect.FromClient, frame("x")); !errors.Is(err, errDead) {
		t.Fatalf("after timeout: %v", err)
	}
}

func TestMemoryLimitFromManifest(t *testing.T) {
	p := load(t, "cfixture_grow.wasm")
	c := p.NewCodec(nil)
	stmts, _, err := c.Decode(inspect.FromClient, frame("1"))
	if err != nil || stmts[0].Text != "grew 1" {
		t.Fatalf("grow within the limit: %v %+v", err, stmts)
	}
	_, _, err = c.Decode(inspect.FromClient, frame("64"))
	if err == nil || !strings.Contains(err.Error(), "memory.grow(64) failed") {
		t.Fatalf("grow past memory_limit_pages: %v", err)
	}
	if size := c.(*codec).in.mem.Size(); size > 4*65536 {
		t.Fatalf("memory is %d bytes, the manifest allowed 4 pages", size)
	}
}

func TestMaskOutsideRewriteTraps(t *testing.T) {
	p := load(t, "cfixture_maskabuse.wasm")
	c := p.NewCodec(nil)
	_, _, err := c.Decode(inspect.FromClient, frame("x"))
	if err == nil || !strings.Contains(err.Error(), "hoop.mask called outside rewrite or flush") {
		t.Fatalf("mask abuse: %v", err)
	}
	if _, _, err := c.Decode(inspect.FromClient, frame("x")); !errors.Is(err, errDead) {
		t.Fatalf("after abuse: %v", err)
	}
}

// closeFailingModule closes the real module and then reports a failure,
// the way wazero does when releasing the module's resources fails.
type closeFailingModule struct{ api.Module }

func (m closeFailingModule) Close(ctx context.Context) error {
	_ = m.Module.Close(ctx)
	return errors.New("boom")
}

func TestModuleCloseFailureIsPartOfTheDeathCause(t *testing.T) {
	p := load(t, "cfixture_trap.wasm")
	c := p.NewCodec(nil).(*codec)
	c.in.mod = closeFailingModule{c.in.mod}
	_, _, err := c.Decode(inspect.FromClient, frame("x"))
	if err == nil || !strings.Contains(err.Error(), "unreachable; module close: boom") {
		t.Fatalf("trap with a failing close: %v", err)
	}
	if _, _, again := c.Decode(inspect.FromClient, frame("x")); !errors.Is(again, errDead) || !strings.Contains(again.Error(), "module close: boom") {
		t.Fatalf("second call: %v", again)
	}
	// The same for a release that is not a death.
	c = p.NewCodec(nil).(*codec)
	c.in.mod = closeFailingModule{c.in.mod}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Decode(inspect.FromClient, frame("x")); !errors.Is(err, errDead) || !strings.Contains(err.Error(), "module close: boom") {
		t.Fatalf("after a failing close: %v", err)
	}
}

func TestCredentialFailureRefusesTheFrame(t *testing.T) {
	p := load(t, "cfixture_credential.wasm")
	for _, text := range []string{"trap", "junk"} {
		c := p.NewCodec(nil)
		stmts, _, err := c.Decode(inspect.FromClient, frame(text))
		if err != nil {
			t.Fatal(err)
		}
		s := stmts[0]
		// ok with an empty credential is what the gate refuses before
		// policy; ok=false would let the frame through on a permissive lane.
		if cred, ok := c.(gate.CredentialSource).TakeCredential(&s); cred != "" || !ok {
			t.Fatalf("%s: TakeCredential returned %q %v", text, cred, ok)
		}
		if s.Operation != inspect.OpUnknown || !strings.HasPrefix(s.Text, "take_credential failed: x-cfix: ") || s.Metadata != nil {
			t.Fatalf("%s: statement after the failure %+v", text, s)
		}
		if !c.(*codec).in.isDead() {
			t.Fatalf("%s: instance survived the failure", text)
		}
		if _, _, err := c.Decode(inspect.FromClient, frame("x")); !errors.Is(err, errDead) {
			t.Fatalf("%s: decode after the failure: %v", text, err)
		}
	}
	// The well-formed answer still lifts.
	c := p.NewCodec(nil)
	stmts, _, _ := c.Decode(inspect.FromClient, frame("hello"))
	s := stmts[0]
	if cred, ok := c.(gate.CredentialSource).TakeCredential(&s); cred != "tok" || !ok || s.Text != "lifted" {
		t.Fatalf("lift: %q %v %+v", cred, ok, s)
	}
}

func TestRefusedOpenReleasesTheInstance(t *testing.T) {
	p := load(t, "cfixture.wasm")
	for i := range 3 {
		c := p.NewCodec(map[string]string{"refuse": "true"}).(*codec)
		if c.in == nil || !c.owned || c.in.mod.IsClosed() {
			t.Fatalf("refused codec %d: in=%v owned=%v", i, c.in, c.owned)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if !c.in.isDead() || !c.in.mod.IsClosed() {
			t.Fatalf("refused codec %d kept its instance after Close", i)
		}
	}
	// Under per_lane the refused connection shares the instance: Close
	// leaves it alive and skips the guest's close for a connection that
	// never opened, which the next connection's counters show.
	lane := load(t, "cfixture_lane.wasm")
	refused := lane.NewCodec(map[string]string{"refuse": "true"}).(*codec)
	if err := refused.Close(); err != nil {
		t.Fatal(err)
	}
	stmts, _, err := lane.NewCodec(nil).Decode(inspect.FromClient, frame("x"))
	if err != nil {
		t.Fatal(err)
	}
	if m := stmts[0].Metadata; m["x-cfix.opens"] != "2" || m["x-cfix.closes"] != "0" {
		t.Fatalf("lane counters after a refused close: %v", m)
	}
}

func TestTrailingPayloadBytesAreRefused(t *testing.T) {
	const manifest = `{"abi":1,"protocol":"x-t","label":"T"}`
	for _, tail := range []string{`{}`, `}`, `]`, `junk`, `[1]`} {
		var res decodeResult
		if err := decodeStrict([]byte(`{"consumed":1}`+tail), &res); err == nil || !strings.Contains(err.Error(), "trailing data") {
			t.Fatalf("decodeStrict with tail %q: %v", tail, err)
		}
		if _, err := parseManifest([]byte(manifest + tail)); err == nil || !strings.Contains(err.Error(), "trailing data") {
			t.Fatalf("parseManifest with tail %q: %v", tail, err)
		}
	}
	var res decodeResult
	if err := decodeStrict([]byte("{\"consumed\":1} \n"), &res); err != nil || res.Consumed != 1 {
		t.Fatalf("whitespace after the value: %v", err)
	}
	if _, err := parseManifest([]byte(manifest + "\n")); err != nil {
		t.Fatalf("whitespace after the manifest: %v", err)
	}
}

// textMasker masks every cell of the "text" column.
type textMasker struct{}

func (textMasker) Mask(data []byte) ([]byte, []string, int) { return data, nil, 0 }
func (textMasker) MaskCell(column string, value []byte) ([]byte, []string, int) {
	if column != "text" || len(value) == 0 {
		return value, nil, 0
	}
	return []byte("***"), []string{"TEXT"}, 1
}

func TestFlushWithoutMaskServesTheMaskImport(t *testing.T) {
	p := load(t, "cfixture_flushmask.wasm")
	ctx := context.Background()
	newGate := func(masker gate.Masker) *gate.Gate {
		t.Helper()
		g, err := gate.New(session.New("x-cfix", session.Identity{Subject: "t"}), gate.Config{
			Protocol:     "x-cfix",
			CodecFactory: func() inspect.Codec { return p.NewCodec(nil) },
			Masker:       masker,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { g.Close(ctx) })
		return g
	}

	// A denied response: the gate discards what the guest holds with no
	// mask, and the guest masks on the way out anyway.
	g := newGate(textMasker{})
	if d := g.Response(ctx, frame("secret")); !d.Allowed || d.Err != nil || len(d.Payload) != 0 {
		t.Fatalf("held response %+v", d)
	}
	g.DiscardResponse()
	d := g.Response(ctx, frame("after"))
	if !d.Allowed || d.Err != nil || len(d.Statements) != 1 || d.Statements[0].Text != "after" {
		t.Fatalf("instance after discard: %+v err %v", d, d.Err)
	}
	// The same release through the masker hands the held chunk to the
	// masker, which replaces it.
	if out := g.FlushResponse(); string(out) != "***" {
		t.Fatalf("flushed %q", out)
	}

	// A lane with no masker flushes with no mask too.
	g = newGate(nil)
	if d := g.Response(ctx, frame("plain")); !d.Allowed || d.Err != nil {
		t.Fatalf("response without masker %+v", d)
	}
	g.DiscardResponse()
	if d := g.Response(ctx, frame("still")); !d.Allowed || d.Err != nil {
		t.Fatalf("instance after discard without masker: %+v err %v", d, d.Err)
	}
}

func TestAnalyzeSQLImport(t *testing.T) {
	p := load(t, "cfixture_sql.wasm")
	c := p.NewCodec(nil)
	stmts, _, err := c.Decode(inspect.FromClient, frame("DELETE FROM Orders WHERE id = 1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"operation":"delete"`, `"effects":["delete"]`, `"relations":[{"name":"orders","access":"write"}]`, `"tables":["orders"]`, `"complete":true`} {
		if !strings.Contains(stmts[0].Text, want) {
			t.Fatalf("analysis %s lacks %s", stmts[0].Text, want)
		}
	}
}

func TestWASIStdoutReachesTheLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p := load(t, "cfixture_wasi.wasm", WithLogger(logger))
	c := p.NewCodec(nil)
	if _, _, err := c.Decode(inspect.FromClient, frame("x")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "cfix says hi") || !strings.Contains(buf.String(), "protocol=x-cfix") {
		t.Fatalf("log: %s", buf.String())
	}
}

func TestGuestLogImport(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p := load(t, "cfixture.wasm", WithLogger(logger))
	p.NewCodec(map[string]string{"refuse": "false"})
	// open logs its options at debug: the defaults merged with the caller's.
	if !strings.Contains(buf.String(), `level=DEBUG msg="{\"refuse\":\"false\"}" protocol=x-cfix conn=0`) {
		t.Fatalf("log: %s", buf.String())
	}
}

func TestOpenRefusalAndOptions(t *testing.T) {
	p := load(t, "cfixture.wasm")
	c := p.NewCodec(map[string]string{"refuse": "true"})
	if _, _, err := c.Decode(inspect.FromClient, frame("x")); err == nil || !strings.Contains(err.Error(), "open refused the connection (code 1)") {
		t.Fatalf("refused open: %v", err)
	}
	if err := c.(*codec).Close(); err != nil {
		t.Fatalf("close of a refused codec: %v", err)
	}
	if err := p.ValidateOptions(map[string]string{"refuse": "maybe"}); err == nil || !strings.Contains(err.Error(), "not true or false") {
		t.Fatalf("bad bool: %v", err)
	}
	if err := p.ValidateOptions(map[string]string{"nope": "1"}); err == nil || !strings.Contains(err.Error(), `unknown option "nope"`) {
		t.Fatalf("unknown option: %v", err)
	}
	c = p.NewCodec(map[string]string{"nope": "1"})
	if _, _, err := c.Decode(inspect.FromClient, frame("x")); err == nil || !strings.Contains(err.Error(), "unknown option") {
		t.Fatalf("unknown option at NewCodec: %v", err)
	}
}

func TestPerConnectionInstancesAreIsolated(t *testing.T) {
	p := load(t, "cfixture.wasm")
	a, b := p.NewCodec(nil), p.NewCodec(nil)
	for i := 0; i < 3; i++ {
		if _, _, err := a.Decode(inspect.FromClient, frame("a")); err != nil {
			t.Fatal(err)
		}
	}
	stmts, _, err := b.Decode(inspect.FromClient, frame("b"))
	if err != nil {
		t.Fatal(err)
	}
	// b's instance never saw a's frames: its sequence and its open count
	// start at one.
	if m := stmts[0].Metadata; m["x-cfix.seq"] != "1" || m["x-cfix.opens"] != "1" || m["x-cfix.conn"] != "0" {
		t.Fatalf("metadata %v", m)
	}
}

func TestPerLaneInstanceIsShared(t *testing.T) {
	p := load(t, "cfixture_lane.wasm")
	a, b := p.NewCodec(nil), p.NewCodec(nil)
	if a.(*codec).in != b.(*codec).in {
		t.Fatal("per_lane codecs do not share the instance")
	}
	for i := 0; i < 3; i++ {
		if _, _, err := a.Decode(inspect.FromClient, frame("a")); err != nil {
			t.Fatal(err)
		}
	}
	stmts, _, err := b.Decode(inspect.FromClient, frame("b"))
	if err != nil {
		t.Fatal(err)
	}
	// One instance, two connections: b is conn 1, both opens are visible,
	// and a's frames leave b's own sequence untouched.
	if m := stmts[0].Metadata; m["x-cfix.seq"] != "1" || m["x-cfix.opens"] != "2" || m["x-cfix.conn"] != "1" {
		t.Fatalf("metadata %v", m)
	}
	if err := a.(*codec).Close(); err != nil {
		t.Fatal(err)
	}
	stmts, _, err = b.Decode(inspect.FromClient, frame("b"))
	if err != nil || stmts[0].Metadata["x-cfix.closes"] != "1" {
		t.Fatalf("close(conn) did not reach the guest: %v %v", err, stmts)
	}
	// The instance outlives a's Close: it belongs to the lane.
	if b.(*codec).in.isDead() {
		t.Fatal("closing one per_lane codec killed the shared instance")
	}
}

func TestPerLaneRebuildsAfterDeath(t *testing.T) {
	p := load(t, "cfixture_lane.wasm")
	a := p.NewCodec(nil)
	// Kill the shared instance from outside, as a trap would.
	a.(*codec).in.die(errors.New("boom"))
	if _, _, err := a.Decode(inspect.FromClient, frame("a")); !errors.Is(err, errDead) {
		t.Fatalf("dead lane: %v", err)
	}
	b := p.NewCodec(nil)
	stmts, _, err := b.Decode(inspect.FromClient, frame("b"))
	if err != nil {
		t.Fatalf("rebuilt lane: %v", err)
	}
	if m := stmts[0].Metadata; m["x-cfix.opens"] != "1" || m["x-cfix.conn"] != "1" {
		t.Fatalf("rebuilt instance metadata %v", m)
	}
}

func TestCloseReleasesTheInstance(t *testing.T) {
	p := load(t, "cfixture.wasm")
	c := p.NewCodec(nil).(*codec)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !c.in.isDead() || !c.in.mod.IsClosed() {
		t.Fatal("Close left the instance alive")
	}
	if _, _, err := c.Decode(inspect.FromClient, frame("x")); !errors.Is(err, errDead) {
		t.Fatalf("after Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Plugin.Close ends every codec still open.
	live := p.NewCodec(nil)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := live.Decode(inspect.FromClient, frame("x")); err == nil {
		t.Fatal("codec survived Plugin.Close")
	}
	if c := p.NewCodec(nil); !errors.Is(c.(*codec).err, errPluginClosed) {
		t.Fatalf("NewCodec after Close: %v", c.(*codec).err)
	}
}

func TestGateAcceptsTheCodec(t *testing.T) {
	p := load(t, "cfixture.wasm")
	factory := func() inspect.Codec { return p.NewCodec(nil) }
	if gate.MaskSupportedBy(factory) {
		t.Fatal("cfixture has no rewrite capability but MaskSupportedBy says yes")
	}
	if _, ok := factory().(analyzer.ContentRenderer); ok {
		t.Fatal("cfixture has no content capability but the codec renders content")
	}
	g, err := gate.New(session.New("x-cfix", session.Identity{Subject: "t"}), gate.Config{Protocol: "x-cfix", CodecFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close(context.Background())
	d := g.Request(context.Background(), frame("hello"))
	if !d.Allowed || len(d.Statements) != 1 || d.Statements[0].Text != "hello" {
		t.Fatalf("decision %+v", d)
	}
	if f, ok := g.DenyFrame(inspect.FromClient, "nope"); !ok || string(f) != "CFIX:nope" {
		t.Fatalf("deny frame %q %v", f, ok)
	}
	// A guest decode error denies the chunk regardless of policy, the way
	// ABI.md says the connection drops; the gate's forward-on-error
	// default is for codecs whose upstream can judge the bytes itself.
	if d = g.Request(context.Background(), []byte{0}); d.Allowed || d.Rule != "stream-unsafe" || !strings.Contains(d.Message, "empty frame") {
		t.Fatalf("decode error decision %+v", d)
	}
	// gate.New refuses a masker on a codec that cannot re-frame.
	_, err = gate.New(session.New("x-cfix", session.Identity{}), gate.Config{Protocol: "x-cfix", CodecFactory: factory, Masker: nopMasker{}})
	if err == nil || !strings.Contains(err.Error(), "cannot re-frame") {
		t.Fatalf("masker on cfixture: %v", err)
	}
}

type nopMasker struct{}

func (nopMasker) Mask(data []byte) ([]byte, []string, int) { return data, nil, 0 }
func (nopMasker) MaskCell(column string, value []byte) ([]byte, []string, int) {
	return value, nil, 0
}
