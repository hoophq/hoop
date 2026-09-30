package daemon

import (
	"strings"
	"testing"
)

func httpLaneWithSensitiveParams() Config {
	cfg := *pgLane()
	cfg.Listeners[0].Protocol = "http"
	cfg.Listeners[0].HTTP = &HTTPCodecConfig{SensitiveQueryParams: []string{"token"}}
	return cfg
}

// A build from before the generated list names no field but review_mode, so
// its reported release stands in: a field that shipped in or before that
// release is granted, a later one is refused, naming both releases.
func TestABuildBeforeTheListIsReadByItsRelease(t *testing.T) {
	cfg := httpLaneWithSensitiveParams()
	for _, hs := range []Handshake{
		{Version: "1.184.2"},
		{Version: "1.190.0", Capabilities: []string{}},
		{Version: "1.197.0", Capabilities: []string{CapabilityReviewMode}},
	} {
		if err := CheckServable(cfg, hs); err != nil {
			t.Errorf("version %q was refused a field it decodes: %v", hs.Version, err)
		}
	}
	for _, hs := range []Handshake{
		{Version: "1.184.1"},
		{Version: "1.183.0", Capabilities: []string{}},
		{Version: "1.100.9", Capabilities: []string{CapabilityReviewMode}},
	} {
		err := CheckServable(cfg, hs)
		if err == nil {
			t.Fatalf("version %q was served a field from a later release", hs.Version)
		}
		for _, want := range []string{`listener "appdb" sets http.sensitive_query_params`, "(" + hs.Version + ")", "1.184.2 or later"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not say %q", err, want)
			}
		}
	}
}

// A release that does not parse grants nothing: "unknown" is a dev build of
// anything, and a preview tag says nothing about what it decodes.
func TestAnUnparseableReleaseGrantsNothing(t *testing.T) {
	cfg := httpLaneWithSensitiveParams()
	for _, v := range []string{"", "unknown", "1878.0.0-gabcdef", "v1.190.0", "1.190"} {
		if err := CheckServable(cfg, Handshake{Version: v}); err == nil {
			t.Errorf("version %q was granted a field by release", v)
		}
	}
}

// A build that sends the generated list is read from the list alone. Its
// release decides nothing, in either direction.
func TestTheGeneratedListOutranksTheRelease(t *testing.T) {
	cfg := httpLaneWithSensitiveParams()
	generated := []string{"rule:operation", "protocol:http"}
	err := CheckServable(cfg, Handshake{Version: "9.9.9", Capabilities: generated})
	if err == nil || !strings.Contains(err.Error(), "sensitive_query_params") {
		t.Errorf("a high release granted a field the list lacks: %v", err)
	}
	listed := append(generated, "sensitive_query_params")
	if err := CheckServable(cfg, Handshake{Version: "unknown", Capabilities: listed}); err != nil {
		t.Errorf("a listed field was refused over the release: %v", err)
	}
}

// A field with no since release is only ever granted by the list, so a build
// before the list is refused it whatever release it reports.
func TestAFieldWithoutASinceReleaseNeedsTheList(t *testing.T) {
	cfg := *pgLane()
	cfg.Analyzer = &AnalyzerConfig{Provider: "stub", Model: "m", RateLimit: &AnalyzerRateLimitConfig{Calls: 1, PerSec: 1}}
	err := CheckServable(cfg, Handshake{Version: "99.0.0", Capabilities: []string{CapabilityReviewMode}})
	if err == nil || !strings.Contains(err.Error(), "rate_limit") {
		t.Errorf("rate_limit was granted by release: %v", err)
	}
	if err := CheckServable(cfg, Handshake{Capabilities: []string{CapabilityAnalyzerRateLimit, "rule:operation"}}); err != nil {
		t.Errorf("a build that lists rate_limit was refused it: %v", err)
	}
}

// Every since tag is a release, and it sits on a cap-tagged field.
func TestSinceTagsAreReleases(t *testing.T) {
	seen := 0
	for _, f := range capFields() {
		if f.since == "" {
			continue
		}
		seen++
		if _, ok := parseRelease(f.since); !ok {
			t.Errorf("%s: since %q is not MAJOR.MINOR.PATCH", f.path, f.since)
		}
	}
	if seen == 0 {
		t.Error("no field carries a since release; review_mode and sensitive_query_params should")
	}
	for _, v := range []string{"1.2.3", "0.0.0", "1878.0.0"} {
		if _, ok := parseRelease(v); !ok {
			t.Errorf("%q should parse", v)
		}
	}
	for _, v := range []string{"1.2", "1.2.3.4", "1.02.3", "a.b.c", "1.2.3-rc1", "unknown", ""} {
		if _, ok := parseRelease(v); ok {
			t.Errorf("%q should not parse", v)
		}
	}
}
