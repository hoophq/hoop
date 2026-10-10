package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// fakeCodec stands in for a plug-in's codec: it records the options it was
// built with and, when asked to, re-frames and renders content, the two
// optional capabilities the daemon reads off a codec by type assertion.
type fakeCodec struct {
	protocol inspect.Protocol
	options  map[string]string
	closed   *int
}

func (c *fakeCodec) Protocol() inspect.Protocol { return c.protocol }
func (c *fakeCodec) Decode(inspect.Direction, []byte) ([]inspect.Statement, int, error) {
	return nil, 0, nil
}
func (c *fakeCodec) Close() error { *c.closed++; return nil }

type reframingCodec struct{ *fakeCodec }

func (reframingCodec) Rewrite([]byte, func(string, []byte) []byte) ([]byte, inspect.ReframeResult, error) {
	return nil, inspect.ReframeResult{}, nil
}
func (reframingCodec) Flush(func(string, []byte) []byte) []byte { return nil }

type renderingCodec struct{ *fakeCodec }

func (renderingCodec) Content(stmt inspect.Statement, maxBytes int) (string, string, bool) {
	return "module rendered " + stmt.Text, "key", stmt.Text != ""
}

// fakeCodecPlugin is the plug-in a test loader returns: the root module
// cannot import codec/wasm, so the tests exercise the contract through its
// interface.
type fakeCodecPlugin struct {
	protocol  inspect.Protocol
	manifest  string
	reframes  bool
	renders   bool
	closed    int
	codecsOut int
}

func (p *fakeCodecPlugin) Protocol() inspect.Protocol { return p.protocol }
func (p *fakeCodecPlugin) Manifest() []byte           { return []byte(p.manifest) }
func (p *fakeCodecPlugin) NewCodec(options map[string]string) inspect.Codec {
	c := &fakeCodec{protocol: p.protocol, options: options, closed: &p.codecsOut}
	switch {
	case p.reframes:
		return reframingCodec{c}
	case p.renders:
		return renderingCodec{c}
	}
	return c
}
func (p *fakeCodecPlugin) Close() error { p.closed++; return nil }

// useFakeLoader installs a loader that answers with plugin for any module
// and restores the build's loader after the test.
func useFakeLoader(t *testing.T, plugin *fakeCodecPlugin, loadErr error) {
	t.Helper()
	prev := LoadCodecPlugin
	LoadCodecPlugin = func(ctx context.Context, module []byte) (CodecPlugin, error) {
		if loadErr != nil {
			return nil, loadErr
		}
		return plugin, nil
	}
	t.Cleanup(func() { LoadCodecPlugin = prev })
}

func writeModule(t *testing.T, body string) (path, digest string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "codec.wasm")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return path, hex.EncodeToString(sum[:])
}

const acmeManifest = `{"abi":1,"protocol":"x-acmewire","label":"Acme Wire","version":"0.1.0","capabilities":["deny","rewrite"]}`

func pluginLaneConfig(module, sha string, extra string) string {
	shaKey := ""
	if sha != "" {
		shaKey = `, "sha256": "` + sha + `"`
	}
	return `{
      "plugins": [{"protocol": "x-acmewire", "module": "` + module + `"` + shaKey + `}],
      "listeners": [{"name": "acme", "protocol": "x-acmewire", "listen": ":1", "upstream": "h:1"` + extra + `}]
    }`
}

// Validation refuses the entries the config alone can check, on the plane
// as on the host, so the loader never fetches a module for a config that
// could not use it.
func TestPluginsEntriesAreValidated(t *testing.T) {
	for name, tc := range map[string]struct{ doc, want string }{
		"bad protocol name": {
			`{"plugins": [{"protocol": "acmewire", "module": "a.wasm"}]}`,
			`protocol "acmewire" must start with "x-"`},
		"uppercase": {
			`{"plugins": [{"protocol": "x-Acme", "module": "a.wasm"}]}`,
			`protocol "x-Acme" must start with "x-"`},
		"no protocol": {
			`{"plugins": [{"module": "a.wasm"}]}`,
			"plugins[0]: no protocol"},
		"no module": {
			`{"plugins": [{"protocol": "x-acme"}]}`,
			"plugins[0] (x-acme): no module"},
		"url without sha": {
			`{"plugins": [{"protocol": "x-acme", "module": "https://example.com/a.wasm"}]}`,
			"needs its sha256"},
		"bad sha": {
			`{"plugins": [{"protocol": "x-acme", "module": "a.wasm", "sha256": "abc"}]}`,
			"not a 64-character hex digest"},
		"duplicate protocol": {
			`{"plugins": [{"protocol": "x-acme", "module": "a.wasm"}, {"protocol": "x-acme", "module": "b.wasm"}]}`,
			`plugins[1]: protocol "x-acme" is already declared by plugins[0]`},
		"lane without entry": {
			`{"listeners": [{"name": "l", "protocol": "x-acme", "listen": ":1", "upstream": "h:1"}]}`,
			`protocol "x-acme" is a plug-in protocol, and no plugins entry declares it`},
		"options on a shipped protocol": {
			`{"listeners": [{"name": "l", "protocol": "postgres", "listen": ":1", "upstream": "h:1", "plugin": {"a": "b"}}]}`,
			`a "plugin" block is only valid on a listener whose protocol a plugins entry declares`},
		"downstream_tls on a plug-in lane": {
			`{"plugins": [{"protocol": "x-acme", "module": "a.wasm"}],
			  "listeners": [{"name": "l", "protocol": "x-acme", "listen": ":1", "upstream": "h:1",
			    "downstream_tls": {"cert_file": "c", "key_file": "k"}}]}`,
			"downstream_tls is only supported on"},
	} {
		t.Run(name, func(t *testing.T) {
			err := CheckConfigBytes([]byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckConfigBytes = %v, want %q", err, tc.want)
			}
		})
	}

	// A declared lane with options passes the config-level check without
	// any module: the plane never loads one.
	ok := `{"plugins": [{"protocol": "x-acme", "module": "https://example.com/a.wasm", "sha256": "` +
		strings.Repeat("ab", 32) + `"}],
	  "listeners": [{"name": "l", "protocol": "x-acme", "listen": ":1", "upstream": "h:1", "plugin": {"deny_prefix": "ACME"}}]}`
	if err := CheckConfigBytes([]byte(ok)); err != nil {
		t.Fatalf("a declared plug-in lane was refused: %v", err)
	}
}

func TestABuildWithoutALoaderRefusesPlugins(t *testing.T) {
	prev := LoadCodecPlugin
	LoadCodecPlugin = nil
	t.Cleanup(func() { LoadCodecPlugin = prev })
	module, sha := writeModule(t, "wasm")
	_, _, err := Setup(writeConfig(t, pluginLaneConfig(module, sha, "")), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "this build loads no codec plug-ins") {
		t.Fatalf("Setup = %v, want the no-loader refusal", err)
	}
}

func TestAShaMismatchRefusesTheModule(t *testing.T) {
	plugin := &fakeCodecPlugin{protocol: "x-acmewire", manifest: acmeManifest}
	useFakeLoader(t, plugin, nil)
	module, _ := writeModule(t, "wasm")
	wrong := strings.Repeat("00", 32)
	_, _, err := Setup(writeConfig(t, pluginLaneConfig(module, wrong, "")), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("Setup = %v, want the digest refusal", err)
	}
	if plugin.closed != 0 {
		t.Error("the module was loaded before its digest was checked")
	}
}

func TestAModuleForAnotherProtocolIsRefused(t *testing.T) {
	plugin := &fakeCodecPlugin{protocol: "x-other", manifest: `{"protocol":"x-other"}`}
	useFakeLoader(t, plugin, nil)
	module, sha := writeModule(t, "wasm")
	_, _, err := Setup(writeConfig(t, pluginLaneConfig(module, sha, "")), nil, nil)
	if err == nil || !strings.Contains(err.Error(), `declares protocol "x-other"; the entry expects "x-acmewire"`) {
		t.Fatalf("Setup = %v, want the protocol refusal", err)
	}
	if plugin.closed != 1 {
		t.Errorf("the refused module was closed %d times, want 1", plugin.closed)
	}
}

func TestALoaderErrorNamesTheEntry(t *testing.T) {
	useFakeLoader(t, nil, errors.New("describe trapped"))
	module, _ := writeModule(t, "wasm")
	_, _, err := Setup(writeConfig(t, pluginLaneConfig(module, "", "")), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "plugins[0] (x-acmewire): describe trapped") {
		t.Fatalf("Setup = %v, want the loader error under the entry", err)
	}
}

// The whole path: the file loads, the module loads, the lane validates and
// builds a codec from the plug-in with the listener's options, and the
// report names the module.
func TestAPluginLaneValidatesAndBuilds(t *testing.T) {
	plugin := &fakeCodecPlugin{protocol: "x-acmewire", manifest: acmeManifest}
	useFakeLoader(t, plugin, nil)
	module, sha := writeModule(t, "wasm")
	cfg, det, err := Setup(writeConfig(t, pluginLaneConfig(module, sha,
		`, "plugin": {"deny_prefix": "ACME"}`)), nil, nil)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if cfg.codecPlugins["x-acmewire"] != plugin {
		t.Fatal("the loaded plug-in is not on the config")
	}

	lanes, err := Validate(cfg, det)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(lanes) != 1 || lanes[0].Protocol != "x-acmewire" {
		t.Fatalf("lanes = %+v", lanes)
	}
	if n := strings.Join(lanes[0].Notes, "\n"); !strings.Contains(n, "codec plug-in x-acmewire 0.1.0, sha256 "+sha+", capabilities: deny, rewrite") {
		t.Errorf("the report does not name the module:\n%s", n)
	}
	var buf bytes.Buffer
	if err := PrintLanes(&buf, cfg.lic, lanes); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "x-acmewire") || !strings.Contains(buf.String(), "codec plug-in") {
		t.Errorf("-validate output does not list the plug-in:\n%s", buf.String())
	}

	built, err := buildLanes(cfg, det, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	codec, ok := built[0].codecFactory().(*fakeCodec)
	if !ok {
		t.Fatalf("the lane's factory built %T, not the plug-in's codec", built[0].codecFactory())
	}
	if codec.options["deny_prefix"] != "ACME" {
		t.Errorf("the listener's options did not reach NewCodec: %v", codec.options)
	}

	// A plug-in without content rendering gets the generic builder, so
	// the analyzer block above validated and classifies something.
	b, ok := analyzer.BuilderFor("x-acmewire")
	if !ok {
		t.Fatal("no analyzer builder after load")
	}
	if _, generic := b.(analyzer.GenericBuilder); !generic {
		t.Errorf("builder = %T, want GenericBuilder", b)
	}

	if err := cfg.closeCodecPlugins(); err != nil {
		t.Fatal(err)
	}
	if plugin.closed != 1 || cfg.codecPlugins != nil {
		t.Errorf("close: plug-in closed %d times, map %v", plugin.closed, cfg.codecPlugins)
	}
}

func TestAContentCapablePluginRendersItsOwnPrompt(t *testing.T) {
	plugin := &fakeCodecPlugin{protocol: "x-render", manifest: `{"protocol":"x-render","capabilities":["content"]}`, renders: true}
	useFakeLoader(t, plugin, nil)
	module, _ := writeModule(t, "wasm")
	cfg, _, err := Setup(writeConfig(t, strings.ReplaceAll(pluginLaneConfig(module, "", ""), "x-acmewire", "x-render")), nil, nil)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer cfg.closeCodecPlugins()
	b, _ := analyzer.BuilderFor("x-render")
	content, ok := b.Build(inspect.Statement{Text: "AUTH"}, 100)
	if !ok || content.Text != "module rendered AUTH" {
		t.Errorf("Build = %+v, %v; the module's rendering did not reach the analyzer", content, ok)
	}
}

// The module decides masking on a plug-in lane: a codec that re-frames
// takes the rules, one that does not refuses them at build, since the
// config check cannot know and the data path would otherwise forward rows
// unmasked under a lane that claims to mask.
func TestMaskingOnAPluginLaneAsksTheModule(t *testing.T) {
	det := stubPlugin{entities: []string{"US_SSN"}}
	mc := MaskConfig{Rules: []byte(`[{"entity":"US_SSN","strategy":"redact"}]`)}
	plain := &fakeCodecPlugin{protocol: "x-plain"}
	if _, err := buildMasker(mc, det, "x-plain", false, codecPluginFactory(plain, nil)); err == nil ||
		!strings.Contains(err.Error(), "declares no rewrite capability") {
		t.Errorf("a non-reframing plug-in took mask rules: %v", err)
	}
	if plain.codecsOut != 1 {
		t.Errorf("the probe codec was closed %d times, want 1", plain.codecsOut)
	}
	rewriting := &fakeCodecPlugin{protocol: "x-rewrite", reframes: true}
	if _, err := buildMasker(mc, det, "x-rewrite", false, codecPluginFactory(rewriting, nil)); err != nil {
		t.Errorf("a re-framing plug-in was refused mask rules: %v", err)
	}

	// At the config level the lane passes: the module answers later.
	doc := `{"plugins": [{"protocol": "x-plain", "module": "a.wasm"}],
	  "listeners": [{"name": "l", "protocol": "x-plain", "listen": ":1", "upstream": "h:1",
	    "mask": {"rules": [{"entity":"US_SSN","strategy":"redact"}]}}]}`
	if err := CheckConfigBytes([]byte(doc)); err != nil {
		t.Errorf("the plane refused mask rules on a plug-in lane: %v", err)
	}
}

// buildLanes refuses a plug-in lane whose module did not load; it never
// binds a port that decodes nothing.
func TestAPluginLaneWithoutItsModuleIsRefusedAtBuild(t *testing.T) {
	cfg := &Config{
		Plugins:   []CodecPluginConfig{{Protocol: "x-acme", Module: "a.wasm"}},
		Listeners: []ListenerConfig{{Name: "l", Protocol: "x-acme", Listen: ":1", Upstream: "h:1"}},
	}
	cfg.codecPlugins = map[inspect.Protocol]CodecPlugin{} // loaded set, without this one
	_, err := buildLanes(cfg, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no loaded plug-in declares it") {
		t.Fatalf("buildLanes = %v", err)
	}
}

// The plugins list is baseline: a document that changes it is a restart,
// never a swap, because the running lanes hold the loaded module.
func TestPluginsAreInTheReloadBaseline(t *testing.T) {
	a, err := LoadConfigBytes([]byte(`{"plugins": [{"protocol": "x-a", "module": "a.wasm"}],
	  "listeners": [{"name": "l", "protocol": "x-a", "listen": ":1", "upstream": "h:1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadConfigBytes([]byte(`{"plugins": [{"protocol": "x-a", "module": "b.wasm"}],
	  "listeners": [{"name": "l", "protocol": "x-a", "listen": ":1", "upstream": "h:1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	da, _ := nonRuleDoc(a)
	db, _ := nonRuleDoc(b)
	if bytes.Equal(da, db) {
		t.Fatal("a module change is not in the baseline; a reload would swap lanes onto a module it never loaded")
	}
}

// The plane grants a plug-in protocol by the plugins capability alone: no
// build reports protocol:x-..., because no build links one.
func TestCheckServableGrantsPluginProtocolsByThePluginsCapability(t *testing.T) {
	cfg := Config{
		Plugins:   []CodecPluginConfig{{Protocol: "x-acme", Module: "https://example.com/a.wasm", SHA256: strings.Repeat("ab", 32)}},
		Listeners: []ListenerConfig{{Name: "acme", Protocol: "x-acme", Listen: ":1", Upstream: "h:1"}},
	}
	with := Handshake{Version: "1.250.0", Capabilities: append(SidecarCapabilities(), "protocol:postgres")}
	if err := CheckServable(cfg, with); err != nil {
		t.Errorf("a build reporting plugins was refused the document: %v", err)
	}
	var without []string
	for _, c := range SidecarCapabilities() {
		if c != CapabilityPlugins {
			without = append(without, c)
		}
	}
	err := CheckServable(cfg, Handshake{Version: "1.250.0", Capabilities: without})
	if err == nil {
		t.Fatal("a build without plugins was served a plug-in lane")
	}
	for _, want := range []string{`listener "acme" speaks the plug-in protocol x-acme`, "sets plugins"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	// Even with the list absent, the lane alone needs the capability.
	lane := Config{Listeners: cfg.Listeners}
	if err := CheckServable(lane, Handshake{Version: "1.250.0", Capabilities: without}); err == nil ||
		!strings.Contains(err.Error(), `"plugins"`) {
		t.Errorf("a plug-in lane without the list was granted: %v", err)
	}
	// The build's own header carries the capability.
	if !strings.Contains(" "+strings.Join(SidecarCapabilities(), " ")+" ", " plugins ") {
		t.Error("this build does not report plugins")
	}
}

func TestCodecTestReadsTheModuleAndFixtures(t *testing.T) {
	prev := CodecTester
	t.Cleanup(func() { CodecTester = prev })

	module, _ := writeModule(t, "wasm bytes")
	fixture := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(fixture, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}

	CodecTester = nil
	if err := CodecTest(context.Background(), module, []string{fixture}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "cannot test codec plug-ins") {
		t.Fatalf("a build without a tester ran: %v", err)
	}

	var gotModule []byte
	var gotFixtures [][]byte
	CodecTester = func(ctx context.Context, m []byte, fx [][]byte, out io.Writer) error {
		gotModule, gotFixtures = m, fx
		_, err := io.WriteString(out, "report\n")
		return err
	}
	var out bytes.Buffer
	if err := CodecTest(context.Background(), module, []string{fixture}, &out); err != nil {
		t.Fatal(err)
	}
	if string(gotModule) != "wasm bytes" || len(gotFixtures) != 1 || string(gotFixtures[0]) != "[]" {
		t.Errorf("tester got module %q fixtures %q", gotModule, gotFixtures)
	}
	if out.String() != "report\n" {
		t.Errorf("report = %q", out.String())
	}
	if err := CodecTest(context.Background(), module, []string{filepath.Join(t.TempDir(), "missing.json")}, io.Discard); err == nil {
		t.Error("a missing fixture file ran")
	}

	// The flag reaches it with the module and the positional fixtures.
	prevArgs, prevVersion := os.Args, Version
	t.Cleanup(func() { os.Args, Version = prevArgs, prevVersion })
	os.Args = []string{"hoop-inspect", "-codec-test", module, fixture, fixture}
	gotFixtures = nil
	if err := Main("test", nil, nil); err != nil {
		t.Fatalf("Main: %v", err)
	}
	if len(gotFixtures) != 2 {
		t.Errorf("Main handed the tester %d fixtures, want 2", len(gotFixtures))
	}
}

// A metadata rule on a plug-in lane passes the local rule check like any
// other lane: nothing about a plug-in protocol narrows the rule vocabulary.
func TestPluginLaneRulesCompile(t *testing.T) {
	doc := `{"plugins": [{"protocol": "x-acme", "module": "a.wasm"}],
	  "listeners": [{"name": "l", "protocol": "x-acme", "listen": ":1", "upstream": "h:1",
	    "guardrails": {"rules": [{"name": "no purge", "type": "` + string(policy.MatchOperation) + `", "operations": ["delete"]}]}}]}`
	if err := CheckConfigBytes([]byte(doc)); err != nil {
		t.Fatalf("CheckConfigBytes: %v", err)
	}
}

// An analyzer on a plug-in lane validates before the module is loaded, and
// on the plane, which never loads one: the load installs the builder.
func TestAnAnalyzerOnAPluginLaneValidatesWithoutTheModule(t *testing.T) {
	cfg := pgLane(aiRule("risky"))
	cfg.Plugins = []CodecPluginConfig{{Protocol: "x-unloaded", Module: "a.wasm"}}
	cfg.Listeners[0].Protocol = "x-unloaded"
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("an ai_analysis rule on a plug-in lane was refused: %v", err)
	}
}
