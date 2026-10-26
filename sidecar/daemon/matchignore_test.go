package daemon

import (
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

func restartIgnore() []analyzer.MatchIgnore {
	return []analyzer.MatchIgnore{{
		Methods:   []string{"PATCH"},
		Resources: []string{"/apis/apps/v1/namespaces/*/deployments/*"},
		Fields:    [][]string{{"spec", "template", "metadata", "annotations", "kubectl.kubernetes.io/restartedAt"}},
	}}
}

// matchIgnoreLane is an http lane that holds PATCH requests and ignores the
// restart timestamp.
func matchIgnoreLane() *Config {
	cfg := holdingLane()
	cfg.Listeners[0].Protocol = "http"
	la := cfg.Listeners[0].Analyzer
	la.Trigger = &policy.AITrigger{Operations: []inspect.Operation{inspect.OpPatch}}
	la.MatchIgnore = restartIgnore()
	return cfg
}

func TestMatchIgnoreOnAnHTTPLaneThatHoldsIsAccepted(t *testing.T) {
	if err := matchIgnoreLane().Validate(); err != nil {
		t.Fatalf("match_ignore on a holding http lane was refused: %v", err)
	}
}

// Each refusal is a key that would load and do nothing, or ignore more than
// it names.
func TestMatchIgnoreIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*Config)
		want string
	}{
		"on a lane that holds nothing": {func(c *Config) {
			c.Listeners[0].Analyzer.HighRisk = RiskLevel{Action: "block"}
			c.Listeners[0].Analyzer.ApprovalRule = ""
		}, "match_ignore decides how a held statement matches its approval request"},
		"with an unscoped item": {func(c *Config) {
			c.Listeners[0].Analyzer.MatchIgnore[0].Resources = nil
		}, "match_ignore[0] names no resources"},
		"with an empty path": {func(c *Config) {
			c.Listeners[0].Analyzer.MatchIgnore[0].Fields = [][]string{{}}
		}, "match_ignore[0] fields[0] is empty"},
		"on a postgres lane": {func(c *Config) {
			c.Listeners[0].Protocol = "postgres"
		}, "sets match_ignore on a postgres lane"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := matchIgnoreLane()
			tc.edit(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate answered %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

// The control plane validates a block alone, then once more on the lane it
// is bound to; the protocol is only known on the second call.
func TestTheControlPlaneChecksMatchIgnoreOnTheBoundLane(t *testing.T) {
	cfg := matchIgnoreLane()
	la := cfg.Listeners[0].Analyzer
	if problems := ValidateLaneAnalyzerBlock(la, "web"); len(problems) != 0 {
		t.Errorf("the block alone was refused: %v", problems)
	}
	lane := cfg.Listeners[0]
	lane.Protocol = "grpc"
	problems := ValidateHoldOnLane(la, lane, "web")
	if len(problems) != 1 || !strings.Contains(problems[0], "only an http lane reads it") {
		t.Errorf("a grpc lane answered %v, want one refusal of match_ignore", problems)
	}
}

// A build that does not report the capability refuses the whole document
// over the key, so the plane must not serve it one.
func TestMatchIgnoreNeedsTheCapability(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), "analyzer_match_ignore") {
		t.Fatalf("the header lacks analyzer_match_ignore: %v", SidecarCapabilities())
	}
	cfg := *matchIgnoreLane()
	if err := CheckServable(cfg, Handshake{Capabilities: []string{CapabilityReviewMode}}); err == nil ||
		!strings.Contains(err.Error(), "analyzer_match_ignore") {
		t.Errorf("a build without the capability was served match_ignore: %v", err)
	}
	if err := CheckServable(cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("this build was refused match_ignore: %v", err)
	}
}

// End to end through the real build path: the lane's match_ignore reaches the
// bytes the plane receives.
func TestTheLaneFilesTheBytesMatchIgnoreLeft(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: highRiskProvider{},
		cp:       cp,
	}
	block := matchIgnoreLane().Listeners[0].Analyzer
	block.ReviewMode = analyzer.ReviewReturn
	pol, err := buildPolicy("web", GuardrailsConfig{}, block, nil, nil, deps)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	resource := "/apis/apps/v1/namespaces/default/deployments/web"
	v := pol.Evaluate(inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Text:      "PATCH " + resource,
		Operation: inspect.OpPatch,
		HTTP: &inspect.HTTPDetail{
			Method: "PATCH", Path: resource, Resource: resource,
			Body: `{"spec":{"template":{"metadata":{"annotations":` +
				`{"kubectl.kubernetes.io/restartedAt":"2026-10-14T10:00:00Z"}}}}}`,
		},
	})

	if !v.Denied || len(*calls) != 1 {
		t.Fatalf("denied=%v filings=%d, want one return-mode denial", v.Denied, len(*calls))
	}
	sent, err := base64.StdEncoding.DecodeString((*calls)[0].payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sent), `"kubectl.kubernetes.io/restartedAt":"<ignored by match_ignore>"`) {
		t.Errorf("the plane received %q, want the timestamp ignored", sent)
	}
	if got := v.Annotations[analyzer.MetadataReviewMatchIgnored]; got != "1" {
		t.Errorf("%s is %q, want 1", analyzer.MetadataReviewMatchIgnored, got)
	}
}
