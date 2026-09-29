package daemon

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/hoophq/hoop/sidecar/analytics"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// The privacy line: nothing an operator typed leaves the process. This test
// plants a distinctive string in every operator-authored field the daemon
// can see and asserts none of them survives into the emitted properties.
// Adding a property that carries one of these fails here.
func TestAnalyticsPropertiesCarryNoOperatorContent(t *testing.T) {
	const planted = "PLANTED_SECRET"
	cfg := &Config{
		Listeners: []ListenerConfig{{
			Name:           planted + "-lane",
			Protocol:       "postgres",
			Listen:         "127.0.0.1:15432",
			Upstream:       planted + ".internal:5432",
			IdentityHeader: "X-" + planted,
		}},
		Guardrails: &GuardrailsConfig{Rules: []policy.Rule{{
			Name:    planted + "-rule",
			Type:    policy.MatchPattern,
			Pattern: planted + "_regex",
			Message: planted + " message",
		}}},
		Analyzer: &AnalyzerConfig{
			Provider: "stub",
			Model:    planted + "-model",
			Endpoint: "https://" + planted + ".llm.internal/v1",
			Prompt:   planted + " prompt",
		},
		Audit:    AuditConfig{File: "/var/log/" + planted + ".jsonl"},
		Admin:    AdminConfig{Listen: "127.0.0.1:19000"},
		LogLevel: "info",
		License:  planted + "-license",
	}
	lanes := []lane{{
		cfg:    cfg.Listeners[0],
		name:   cfg.Listeners[0].Name,
		rules:  []string{planted + "-rule"},
		opaURL: "http://" + planted + ".opa:8181/v1/data",
	}}

	tel := &telemetry{}
	for name, props := range map[string]map[string]any{
		"boot":  tel.bootProperties(cfg),
		"shape": shapeProperties(cfg, lanes, nil),
	} {
		raw, err := json.Marshal(props)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), planted) {
			t.Errorf("%s properties carry operator content: %s", name, raw)
		}
	}

	shape := shapeProperties(cfg, lanes, nil)
	for _, forbidden := range []string{"analyzer-model", "analyzer-endpoint", "analyzer-prompt"} {
		if _, ok := shape[forbidden]; ok {
			t.Errorf("shape carries %q", forbidden)
		}
	}
	// What the analyzer block IS allowed to say: which registered provider,
	// how it sends, how it fails, and whether a prompt exists.
	for _, allowed := range []string{"analyzer-provider", "analyzer-send", "analyzer-fail-open", "analyzer-custom-prompt"} {
		if _, ok := shape[allowed]; !ok {
			t.Errorf("shape is missing %q", allowed)
		}
	}
	if shape["analyzer-custom-prompt"] != true {
		t.Error("a configured prompt must be reported as present, never as text")
	}
}

// The stopped event names the class of a listener failure, never the
// address in its message.
func TestListenerErrorKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"bind wrapped by net", &net.OpError{Op: "listen", Err: syscall.EADDRINUSE}, "bind"},
		{"bind wrapped by fmt", fmt.Errorf("appdb: %w", syscall.EACCES), "bind"},
		{"cert", &tls.CertificateVerificationError{Err: errors.New("x")}, "tls"},
		{"tls by message", errors.New("tls: failed to load cert_file"), "tls"},
		{"other", errors.New("upstream vanished"), "other"},
	} {
		if got := listenerErrorKind(tc.err); got != tc.want {
			t.Errorf("%s: kind = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A reload's `changed` list names exactly the section that moved.
func TestLaneSectionDocsNameTheSectionThatMoved(t *testing.T) {
	base := func() *Config {
		return &Config{
			Listeners: []ListenerConfig{{Name: "appdb", Protocol: "postgres", Listen: ":1", Upstream: "h:1"}},
			Guardrails: &GuardrailsConfig{Rules: []policy.Rule{{
				Name: "no-drop", Type: policy.MatchOperation, Operations: []inspect.Operation{inspect.OpDrop},
			}}},
			Mask: &MaskConfig{Rules: json.RawMessage(`[{"entities":["EMAIL_ADDRESS"]}]`)},
		}
	}
	before, after := base(), base()
	after.Mask.Rules = json.RawMessage(`[{"entities":["EMAIL_ADDRESS","BR_CPF"]}]`)

	prev, err := laneSectionDocs(before, before.Listeners[0])
	if err != nil {
		t.Fatal(err)
	}
	next, err := laneSectionDocs(after, after.Listeners[0])
	if err != nil {
		t.Fatal(err)
	}
	var changed []string
	for name := range next {
		if !bytes.Equal(prev[name], next[name]) {
			changed = append(changed, name)
		}
	}
	if len(changed) != 1 || changed[0] != "mask" {
		t.Fatalf("changed = %v, want [mask]", changed)
	}
}

// buildPolicy composes Chain and Observe; the collector must find the
// analyzer at any depth and nothing else.
func TestCollectAnalyzersFindsEvaluatorsInsideChainAndObserve(t *testing.T) {
	ev, err := analyzer.New(analyzer.Config{Rule: "risky", Provider: stubAnalyzerProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := policy.NewRules([]policy.Rule{{
		Name: "no-drop", Type: policy.MatchOperation, Operations: []inspect.Operation{inspect.OpDrop},
	}})
	chain := policy.Observe{Evaluator: policy.Chain{rules, policy.Chain{ev}}}

	got := collectAnalyzers(chain)
	if len(got) != 1 || got[0] != ev {
		t.Fatalf("collectAnalyzers = %v, want the one evaluator", got)
	}
	if collectAnalyzers(nil) != nil || collectAnalyzers(rules) != nil {
		t.Fatal("a chain with no analyzer must collect none")
	}
}

// An evaluator a reload swaps out must not take its last window's work with
// it: the reloader banks the delta, and the next usage event carries it.
// Then the new instance starts from zero, never negative.
func TestRetiredAnalyzersReachTheNextUsageEvent(t *testing.T) {
	newEv := func() *analyzer.Evaluator {
		ev, err := analyzer.New(analyzer.Config{
			Rule: "risky", Provider: stubAnalyzerProvider{},
			Trigger: analyzer.Trigger{All: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	// A request on a protocol with a content builder: what classify accepts.
	stmt := inspect.Statement{
		Protocol: inspect.Postgres, Direction: inspect.FromClient,
		Operation: inspect.OpSelect, Text: "SELECT 1",
	}

	tel := &telemetry{
		client:    &analytics.Client{}, // disabled; we read the properties directly
		counters:  analytics.NewCounters(),
		conns:     map[string]connSnapshot{},
		analyzers: map[*analyzer.Evaluator]analyzer.Stats{},
	}
	old := newEv()
	old.Evaluate(stmt)
	old.Evaluate(stmt)
	oldCalls := old.Stats().Calls
	if oldCalls == 0 {
		t.Fatal("fixture: the retired evaluator made no calls")
	}

	// A reload retires old before any usage event baselined it.
	tel.retireAnalyzers([]*analyzer.Evaluator{old})
	fresh := newEv()
	fresh.Evaluate(stmt)
	freshCalls := fresh.Stats().Calls

	got := tel.usageProperties(nil, []lane{{analyzers: []*analyzer.Evaluator{fresh}}})
	if want := oldCalls + freshCalls; got["analyzer-calls"] != want {
		t.Fatalf("analyzer-calls = %v, want %d (%d banked from the retired instance + %d live)",
			got["analyzer-calls"], want, oldCalls, freshCalls)
	}
	if _, still := tel.analyzers[old]; still {
		t.Fatal("retired evaluator still tracked")
	}

	// Next window: nothing new happened; the bank is empty and fresh's
	// baseline holds, so the delta is zero, not negative.
	got = tel.usageProperties(nil, []lane{{analyzers: []*analyzer.Evaluator{fresh}}})
	if got["analyzer-calls"] != int64(0) {
		t.Fatalf("second window analyzer-calls = %v, want 0", got["analyzer-calls"])
	}
}

// A grpc RPC opened before a reload keeps its gate, and so its analyzer,
// after the lane swaps. What that analyzer does after the swap must still
// reach usage: counted while the RPC is open, banked once when it closes,
// and never counted again after that.
func TestAnAnalyzerStillOpenAcrossAReloadIsCounted(t *testing.T) {
	desc := writeGRPCTestDescriptors(t)
	base := fmt.Sprintf(`{
  "analyzer": {"provider": "stub", "model": "m"},
  "listeners": [{
    "name": "g", "protocol": "grpc",
    "listen": "127.0.0.1:0", "upstream": "h:50051",
    "grpc": {"capture_payload": true, "descriptors": [%q]},
    "analyzer": {"trigger": {"resources": ["/test.v1.Echo/**"]}, "high": "block", "cache": {"size": 16, "ttl_sec": 60}}
  }],
  "audit": {"file": "-"}
}`, desc)
	cfg, err := LoadConfigBytes([]byte(base))
	if err != nil {
		t.Fatalf("LoadConfigBytes: %v", err)
	}
	cfg.cp = &controlPlane{url: "http://plane", token: "hsc_x", lastRaw: []byte(base)}
	ac, err := setupAnalyzer(cfg, nil)
	if err != nil {
		t.Fatalf("setupAnalyzer: %v", err)
	}
	lanes, err := buildLanes(cfg, nil, ac)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	if len(lanes[0].analyzers) == 0 {
		t.Fatal("fixture: the lane built no analyzer")
	}
	old := lanes[0].analyzers[0]
	rules := newLiveRules(lanes[0])
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{lanes: lanes})
	rl, err := newReloader(cfg, lanes, map[string]ruleSwapper{"g": rules}, nil, ac, view,
		newLicenseState(cfg.lic, cfg.dependsOnLicense()))
	if err != nil {
		t.Fatalf("newReloader: %v", err)
	}
	tel := &telemetry{
		client:    &analytics.Client{}, // disabled; we read the properties directly
		counters:  analytics.NewCounters(),
		conns:     map[string]connSnapshot{},
		analyzers: map[*analyzer.Evaluator]analyzer.Stats{},
	}
	rl.tel = tel

	// An RPC opened under the first generation.
	_, release := rules.acquire()

	var buf bytes.Buffer
	drifted := strings.Replace(base, `"high": "block"`, `"high": "warn"`, 1)
	if got := handleWith(rl, &buf, drifted); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, &buf)
	}
	live := view.Load().lanes
	fresh := live[0].analyzers[0]
	if fresh == old {
		t.Fatal("fixture: the reload did not rebuild the evaluator")
	}
	window := func() analytics.Properties { return tel.usageProperties(nil, live) }

	// The open RPC evaluates a message through its old gate, and a new RPC
	// one through the new gate. The two instances share the lane's call
	// counter, so this window holds two calls, not three or four.
	msg := inspect.Statement{
		Protocol: inspect.GRPC, Direction: inspect.FromClient,
		HTTP: &inspect.HTTPDetail{Resource: "/test.v1.Echo/Say", Body: `{"a":1}`},
	}
	other := msg
	other.HTTP = &inspect.HTTPDetail{Resource: "/test.v1.Echo/Say", Body: `{"a":2}`}
	old.Evaluate(msg)
	fresh.Evaluate(other)
	if old.Stats().Calls != 2 {
		t.Fatal("fixture: old and new evaluator do not share one call counter")
	}
	if got := window()["analyzer-calls"]; got != int64(2) {
		t.Fatalf("window with the RPC open: analyzer-calls = %v, want 2", got)
	}

	// The open RPC repeats its message: a cache hit on the OLD instance,
	// which only the drain keeps readable. Then the RPC ends.
	old.Evaluate(msg)
	if old.Stats().CacheHits != 1 {
		t.Fatal("fixture: the repeated message was not a cache hit")
	}
	release()
	got := window()
	if got["analyzer-cache-hits"] != int64(1) || got["analyzer-calls"] != int64(0) {
		t.Fatalf("window after the RPC closed: cache-hits = %v, calls = %v; want 1, 0",
			got["analyzer-cache-hits"], got["analyzer-calls"])
	}
	if _, still := tel.analyzers[old]; still {
		t.Fatal("the drained evaluator is still tracked")
	}
	if _, still := tel.draining[old]; still {
		t.Fatal("the drained evaluator is still draining")
	}
	got = window()
	if got["analyzer-cache-hits"] != int64(0) || got["analyzer-calls"] != int64(0) {
		t.Fatalf("window after it was banked: cache-hits = %v, calls = %v; want 0, 0",
			got["analyzer-cache-hits"], got["analyzer-calls"])
	}
}

// config-format names the document that is authoritative: a plane-served
// config is JSON whatever the local file is; the file's extension counts
// only when the file runs.
func TestConfigFormatFollowsTheAuthoritativeSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		cp   *controlPlane
		path string
		want string
	}{
		{"standalone yaml", nil, "cfg.yaml", "yaml"},
		{"standalone json", nil, "cfg.json", "json"},
		{"plane owns, yaml on disk", &controlPlane{}, "cfg.yaml", "json"},
		{"plane owns, no file", &controlPlane{}, "", "json"},
		{"plane delegated to disk", &controlPlane{diskMode: true}, "cfg.yaml", "yaml"},
	} {
		cfg := &Config{cp: tc.cp}
		setConfigFormat(cfg, tc.path)
		if cfg.configFormat != tc.want {
			t.Errorf("%s: config-format = %q, want %q", tc.name, cfg.configFormat, tc.want)
		}
	}
}

// A standalone install's id follows the host and the config FILE, so the
// edits an operator makes routinely — renaming a listener, adding one,
// changing rules — keep the same Segment profile. A different file on the
// same host is a different install; the operator's own id wins over both.
func TestStandaloneIDSurvivesConfigEdits(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	idFor := func(cfg *Config) string {
		tel := newTelemetry(cfg, log)
		defer tel.close()
		return tel.client.ID()
	}
	base := func() *Config {
		return &Config{
			configPath: "/etc/hoop-inspect/config.yaml",
			Listeners:  []ListenerConfig{{Name: "appdb", Protocol: "postgres", Listen: ":15432", Upstream: "h:1"}},
		}
	}
	want := idFor(base())
	if want == "" {
		t.Fatal("no id")
	}

	renamed := base()
	renamed.Listeners[0].Name = "reporting"
	added := base()
	added.Listeners = append(added.Listeners, ListenerConfig{Name: "api", Protocol: "http", Listen: ":8080", Upstream: "h:2"})
	moved := base()
	moved.Listeners[0].Listen = ":15433"
	for name, cfg := range map[string]*Config{"renamed": renamed, "added listener": added, "moved port": moved} {
		if got := idFor(cfg); got != want {
			t.Errorf("%s changed the id", name)
		}
	}

	other := base()
	other.configPath = "/etc/hoop-inspect/other.yaml"
	if idFor(other) == want {
		t.Error("a different config file on the same host must be a different install")
	}

	t.Setenv(analytics.IDEnvVar, "billing-proxy")
	if idFor(base()) != analytics.IDFromEnv() {
		t.Error("the operator's id must win")
	}
}
