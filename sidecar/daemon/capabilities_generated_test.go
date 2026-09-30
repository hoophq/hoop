package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/policy"
)

// The header is generated from what the build links: every cap-tagged field,
// every rule type and every protocol. Nobody lists an entry by hand.
func TestTheHeaderIsGeneratedFromTheBuild(t *testing.T) {
	caps := SidecarCapabilities()
	if !slices.IsSorted(caps) {
		t.Errorf("the header is not sorted: %v", caps)
	}
	want := []string{CapabilityReviewMode, "protocol:grpc", "protocol:spanner", "protocol:ssh"}
	for _, mt := range policy.RuleTypes() {
		want = append(want, "rule:"+string(mt))
	}
	for _, p := range Protocols() {
		want = append(want, "protocol:"+p)
	}
	for _, w := range want {
		if !slices.Contains(caps, w) {
			t.Errorf("the header lacks %q: %v", w, caps)
		}
	}
	for _, c := range caps {
		if strings.Count(strings.Join(caps, ","), c) != 1 {
			t.Errorf("%q is reported twice: %v", c, caps)
		}
	}
}

// The baseline is what a build from before the generated list decodes. It
// may only ever shrink relative to the build: an entry the build does not
// know would be granted to old builds that cannot decode it either.
func TestTheBaselineIsFrozenBelowTheBuild(t *testing.T) {
	known := map[string]bool{}
	for _, mt := range policy.RuleTypes() {
		known["rule:"+string(mt)] = true
	}
	for _, p := range []string{"clickhouse", "grpc", "http", "mongodb", "mssql", "mysql", "postgres", "spanner", "ssh"} {
		known["protocol:"+p] = true
	}
	for _, b := range baselineCapabilities {
		if !known[b] {
			t.Errorf("baseline names %q, which this build does not construct", b)
		}
	}
}

func rulesLane(rules ...policy.Rule) Config {
	cfg := *pgLane()
	cfg.Listeners[0].Guardrails = &GuardrailsConfig{Rules: rules}
	return cfg
}

// A build whose header predates the rule: and protocol: entries decodes the
// frozen baseline, so a document made of today's vocabulary is still served
// to it, and only a value newer than the baseline is refused.
func TestAnOldHeaderKeepsTheBaselineVocabulary(t *testing.T) {
	cfg := rulesLane(policy.Rule{Name: "hdr", Type: policy.MatchHTTPHeader})
	for _, caps := range [][]string{nil, {}, {CapabilityReviewMode}} {
		if err := CheckServable(cfg, Handshake{Capabilities: caps}); err != nil {
			t.Errorf("capabilities %v were refused today's vocabulary: %v", caps, err)
		}
	}

	future := rulesLane(policy.Rule{Name: "next", Type: "future_type"})
	for _, caps := range [][]string{nil, {}, {CapabilityReviewMode}, {"rule:operation"}} {
		err := CheckServable(future, Handshake{Capabilities: caps})
		if err == nil {
			t.Fatalf("capabilities %v were served a rule type they do not list", caps)
		}
		for _, want := range []string{`listener "appdb"`, `"next"`, "future_type"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name %q", err, want)
			}
		}
	}
	if err := CheckServable(future, Handshake{Capabilities: []string{"rule:future_type"}}); err != nil {
		t.Errorf("a build that reports the type was refused it: %v", err)
	}
}

// A header that carries rule: entries is complete: a type it omits is one the
// build refuses at Validate, so the plane refuses it first, naming the rule.
func TestARuleTypeNeedsItsEntryOnceTheHeaderListsAny(t *testing.T) {
	cfg := rulesLane(
		policy.Rule{Name: "words", Type: policy.MatchDenyWords, Words: []string{"drop"}},
		policy.Rule{Name: "hdr", Type: policy.MatchHTTPHeader},
	)
	err := CheckServable(cfg, Handshake{Capabilities: []string{"rule:deny_words_list", "protocol:postgres"}})
	if err == nil || !strings.Contains(err.Error(), `binds the http_header rule "hdr"`) {
		t.Errorf("a missing rule type was not refused by name: %v", err)
	}
	if strings.Contains(err.Error(), `"words"`) {
		t.Errorf("a listed rule type was refused: %v", err)
	}
	// The deprecated policy spelling is still a rule list the plane can hold.
	cfg.Listeners[0].Guardrails = nil
	cfg.Listeners[0].Policy = &PolicyConfig{Rules: []policy.Rule{{Name: "old", Type: policy.MatchTable}}}
	err = CheckServable(cfg, Handshake{Capabilities: []string{"rule:deny_words_list", "protocol:postgres"}})
	if err == nil || !strings.Contains(err.Error(), `"old"`) {
		t.Errorf("a rule under the deprecated policy key was not checked: %v", err)
	}
}

func TestAProtocolNeedsItsEntryOnceTheHeaderListsAny(t *testing.T) {
	cfg := *pgLane()
	if err := CheckServable(cfg, Handshake{Capabilities: []string{"protocol:postgres"}}); err != nil {
		t.Errorf("a listed protocol was refused: %v", err)
	}
	err := CheckServable(cfg, Handshake{Capabilities: []string{"protocol:mysql"}})
	if err == nil || !strings.Contains(err.Error(), `listener "appdb" speaks postgres`) {
		t.Errorf("a missing protocol was not refused by name: %v", err)
	}
	if !strings.Contains(err.Error(), `"protocol:postgres"`) {
		t.Errorf("the refusal does not name the entry to look for: %v", err)
	}
}

// Every field tag reaches the check through the walk, so a set field is
// refused wherever it sits and a zero one never is.
func TestACapTaggedFieldIsRefusedOnlyWhenSet(t *testing.T) {
	cfg := *holdingLane()
	cfg.Listeners[0].Analyzer.ReviewMode = "return"
	err := CheckServable(cfg, Handshake{Capabilities: []string{"rule:operation", "protocol:postgres"}})
	if err == nil || !strings.Contains(err.Error(), `listener "appdb" sets analyzer.review_mode`) ||
		!strings.Contains(err.Error(), capabilitySince()[CapabilityReviewMode]) {
		t.Errorf("a set cap field was not refused by path and release: %v", err)
	}
	cfg.Listeners[0].Analyzer.ReviewMode = "hold"
	if err := CheckServable(cfg, Handshake{Capabilities: []string{"rule:operation", "protocol:postgres"}}); err != nil {
		t.Errorf("the default was refused: %v", err)
	}
}
