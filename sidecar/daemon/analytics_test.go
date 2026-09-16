package daemon

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
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
