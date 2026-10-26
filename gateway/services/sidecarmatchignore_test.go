package services

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/daemon"
)

func restartIgnore(resource string) []analyzer.MatchIgnore {
	return []analyzer.MatchIgnore{{
		Resources: []string{resource},
		Fields:    [][]string{{"spec", "template", "metadata", "annotations", "kubectl.kubernetes.io/restartedAt"}},
	}}
}

// The rule owns match_ignore when it names one, like review_mode; otherwise
// the listener's stays. Dropping it would serve a lane that files exact bytes
// while the admin's rule says otherwise.
func TestAnAnalyzerRuleMatchIgnoreWinsWhenSet(t *testing.T) {
	listener := restartIgnore("/apis/apps/v1/namespaces/*/deployments/*")
	rule := restartIgnore("/apis/apps/v1/namespaces/*/statefulsets/*")
	base := &daemon.LaneAnalyzerConfig{MatchIgnore: listener}
	if got := mergeAnalyzerBlock(base, daemon.LaneAnalyzerConfig{}); !reflect.DeepEqual(got.MatchIgnore, listener) {
		t.Errorf("a rule naming no match_ignore dropped the listener's: %+v", got.MatchIgnore)
	}
	if got := mergeAnalyzerBlock(base, daemon.LaneAnalyzerConfig{MatchIgnore: rule}); !reflect.DeepEqual(got.MatchIgnore, rule) {
		t.Errorf("the rule's match_ignore did not win: %+v", got.MatchIgnore)
	}
}

// A listener's own block can set match_ignore on a lane that is not http, and
// a bound rule then makes it hold. The sidecar refuses that document as a
// whole, so the plane refuses it first.
func TestAComposedMatchIgnoreOnANonHTTPLaneIsReported(t *testing.T) {
	base := &daemon.LaneAnalyzerConfig{MatchIgnore: restartIgnore("/a")}
	cfg := daemon.Config{
		Analyzer: &daemon.AnalyzerConfig{Provider: "anthropic", Model: "m"},
		Listeners: []daemon.ListenerConfig{{
			Name: "appdb", Protocol: "postgres",
			Analyzer: mergeAnalyzerBlock(base, daemon.LaneAnalyzerConfig{HighRisk: daemon.RiskLevel{Action: "require_review"}, ApprovalRule: "dba"}),
		}},
	}
	err := validateComposedAnalyzers(cfg)
	if err == nil || !strings.Contains(err.Error(), "only an http lane reads it") {
		t.Fatalf("the composed block was accepted: %v", err)
	}

	cfg.Listeners[0].Protocol = "http"
	if err := validateComposedAnalyzers(cfg); err != nil {
		t.Errorf("the same block on an http lane was refused: %v", err)
	}
}

// match_ignore travels with the decision on import: left in the base, the
// base would name it on a lane that holds nothing.
func TestSplitSidecarConfigurationMovesMatchIgnoreToTheRule(t *testing.T) {
	ignore := restartIgnore("/apis/apps/v1/namespaces/*/deployments/*")
	cfg := daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name: "k8s", Protocol: "http",
		Analyzer: &daemon.LaneAnalyzerConfig{HighRisk: daemon.RiskLevel{Action: "require_review"}, ApprovalRule: "ops", MatchIgnore: ignore},
	}}}

	stripped, items, err := SplitSidecarConfiguration("edge", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a := stripped.Listeners[0].Analyzer; a != nil && len(a.MatchIgnore) != 0 {
		t.Errorf("the lane base kept match_ignore: %+v", a.MatchIgnore)
	}
	var found bool
	for _, it := range items {
		if it.Kind != SidecarRuleAnalyzer {
			continue
		}
		found = true
		var spec daemon.LaneAnalyzerConfig
		if err := json.Unmarshal(it.Spec, &spec); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(spec.MatchIgnore, ignore) {
			t.Errorf("the analyzer rule carries match_ignore %+v, want %+v", spec.MatchIgnore, ignore)
		}
	}
	if !found {
		t.Fatal("the import produced no analyzer rule")
	}
}
