package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func reviewModeLane(mode analyzer.ReviewMode) *Config {
	cfg := holdingLane()
	cfg.Listeners[0].Analyzer.ReviewMode = mode
	return cfg
}

func TestReviewModeIsValidated(t *testing.T) {
	for _, mode := range []analyzer.ReviewMode{"", analyzer.ReviewHold, analyzer.ReviewReturn} {
		if err := reviewModeLane(mode).Validate(); err != nil {
			t.Errorf("review_mode %q was refused: %v", mode, err)
		}
	}

	err := reviewModeLane("later").Validate()
	if err == nil || !strings.Contains(err.Error(), `unknown review_mode (or approval_mode) "later"`) {
		t.Errorf("an unknown review_mode was not refused: %v", err)
	}

	la := laneBlock()
	la.ReviewMode = analyzer.ReviewReturn
	err = blockLane(la).Validate()
	if err == nil || !strings.Contains(err.Error(), "nothing on this lane would hold one") {
		t.Errorf("review_mode on a lane that holds nothing was not refused: %v", err)
	}
}

// A build that predates review_mode decodes the served document strictly, so
// the key must be absent unless a lane actually returns.
func TestAHoldLaneServesNoReviewModeKey(t *testing.T) {
	for _, mode := range []analyzer.ReviewMode{"", analyzer.ReviewHold} {
		stored := reviewModeLane(mode)
		raw, err := json.Marshal(ServedForm(*stored))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "review_mode") {
			t.Errorf("review_mode %q served the key: %s", mode, raw)
		}
		if stored.Listeners[0].Analyzer.ReviewMode != mode {
			t.Errorf("ServedForm changed the stored block to %q", stored.Listeners[0].Analyzer.ReviewMode)
		}
	}

	raw, _ := json.Marshal(ServedForm(*reviewModeLane(analyzer.ReviewReturn)))
	if !strings.Contains(string(raw), `"review_mode":"return"`) {
		t.Errorf("a return lane lost its mode: %s", raw)
	}
}

func TestReturnIsServedOnlyToABuildThatDecodesIt(t *testing.T) {
	cfg := *reviewModeLane(analyzer.ReviewReturn)

	if err := CheckServable(cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("a current build was refused: %v", err)
	}
	for _, caps := range [][]string{nil, {}, {"something_else"}} {
		err := CheckServable(cfg, Handshake{Capabilities: caps})
		if err == nil {
			t.Fatalf("capabilities %v were served review_mode", caps)
		}
		for _, want := range []string{cfg.Listeners[0].Name, capabilitySince()[CapabilityReviewMode]} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name %q", err, want)
			}
		}
	}
	if err := CheckServable(*reviewModeLane(analyzer.ReviewHold), Handshake{Capabilities: nil}); err != nil {
		t.Errorf("a hold lane was refused to an old build: %v", err)
	}
}

// A build that predates rate_limit refuses the whole document over the key,
// on the top-level section as much as on a lane's block.
func TestRateLimitIsServedOnlyToABuildThatDecodesIt(t *testing.T) {
	rate := &AnalyzerRateLimitConfig{Calls: 30, PerSec: 60}
	top := blockLane(laneBlock())
	top.Analyzer.RateLimit = rate
	lane := blockLane(laneBlock())
	lane.Listeners[0].Analyzer.RateLimit = rate

	for name, cfg := range map[string]*Config{"top level": top, "lane": lane} {
		if err := CheckServable(*cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
			t.Errorf("%s: a current build was refused: %v", name, err)
		}
		err := CheckServable(*cfg, Handshake{Capabilities: []string{CapabilityReviewMode}})
		if err == nil {
			t.Fatalf("%s: rate_limit was served to a build that cannot decode it", name)
		}
		if !strings.Contains(err.Error(), CapabilityAnalyzerRateLimit) {
			t.Errorf("%s: refusal %q does not name the entry", name, err)
		}
	}
	if err := CheckServable(*blockLane(laneBlock()), Handshake{Capabilities: nil}); err != nil {
		t.Errorf("a config with no rate_limit was refused to an old build: %v", err)
	}
}

func TestParseCapabilities(t *testing.T) {
	if got := ParseCapabilities(""); got == nil || len(got) != 0 {
		t.Errorf("an empty header parsed to %#v, want an empty non-nil list", got)
	}
	got := ParseCapabilities(" review_mode , ,next")
	if len(got) != 2 || got[0] != "review_mode" || got[1] != "next" {
		t.Errorf("parsed %#v", got)
	}
}

// End to end through the lane: a return lane files once, answers the pending
// review at once and never claims.
func TestAReturnLaneAnswersAPendingReviewAtOnce(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: highRiskProvider{},
		cp:       cp,
	}
	la := laneBlock()
	la.HighRisk = "require_review"
	la.ApprovalRule = "payments-approvers"
	la.ReviewMode = analyzer.ReviewReturn

	pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	v := pol.Evaluate(inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM users",
		Operation: inspect.OpDelete,
		Tables:    []string{"users"},
	})

	if !v.Denied || !strings.Contains(v.Message, "9f97") || !strings.Contains(v.Message, "resend") {
		t.Fatalf("denied=%v message=%q, want an immediate denial naming the review", v.Denied, v.Message)
	}
	if len(*calls) != 1 {
		t.Errorf("the plane saw %d requests, want 1 filing and no claim", len(*calls))
	}
}
