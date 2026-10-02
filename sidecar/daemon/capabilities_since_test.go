package daemon

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/policy"
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

// A build that sends the generated list is granted a field by its release
// as well: 1.211.0 lists no trust, yet decodes it (EVL-338). A release
// before the field, or one that does not parse, gets it from the list alone.
func TestTheReleaseGrantsAFieldTheListLacks(t *testing.T) {
	cfg := *pgLane()
	cfg.Trust = &TrustConfig{CAFile: "/ca.pem"}
	generated := []string{"rule:operation", "protocol:postgres", CapabilityReviewMode}
	if err := CheckServable(cfg, Handshake{Version: "1.211.0", Capabilities: generated}); err != nil {
		t.Errorf("a release that decodes trust was refused it: %v", err)
	}
	for _, v := range []string{"1.197.0", "unknown"} {
		err := CheckServable(cfg, Handshake{Version: v, Capabilities: generated})
		if err == nil || !strings.Contains(err.Error(), "trust") {
			t.Errorf("version %q was granted trust its list lacks: %v", v, err)
		}
	}
	listed := append(generated, "trust")
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

// A baseline entry that shipped after 1.162.0, the first release that
// handshakes, is granted to a build before the list by its release, like a
// since-tagged field: the http_header rule type from 1.194.0, the ssh
// protocol from 1.176.0. An older entry is granted whatever the release says.
func TestABaselineEntryIsGrantedFromItsRelease(t *testing.T) {
	hdr := rulesLane(policy.Rule{Name: "hdr", Type: policy.MatchHTTPHeader})
	for _, hs := range []Handshake{
		{Version: "1.194.0"},
		{Version: "1.195.0", Capabilities: []string{}},
		{Version: "1.200.0", Capabilities: []string{CapabilityReviewMode}},
	} {
		if err := CheckServable(hdr, hs); err != nil {
			t.Errorf("version %q was refused a rule type it decodes: %v", hs.Version, err)
		}
	}
	for _, hs := range []Handshake{{Version: "1.193.0"}, {Version: "1.184.1", Capabilities: []string{}}, {Version: "unknown"}} {
		err := CheckServable(hdr, hs)
		if err == nil {
			t.Fatalf("version %q was served a rule type from a later release", hs.Version)
		}
		for _, want := range []string{`binds the http_header rule "hdr"`, "1.194.0 or later"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not say %q", err, want)
			}
		}
	}

	ssh := *pgLane()
	ssh.Listeners[0].Protocol = "ssh"
	if err := CheckServable(ssh, Handshake{Version: "1.176.0"}); err != nil {
		t.Errorf("1.176.0 was refused ssh: %v", err)
	}
	if err := CheckServable(ssh, Handshake{Version: "1.175.0"}); err == nil || !strings.Contains(err.Error(), "1.176.0 or later") {
		t.Errorf("1.175.0 was served ssh, or the refusal does not name the release: %v", err)
	}

	old := rulesLane(policy.Rule{Name: "t", Type: policy.MatchTable})
	for _, hs := range []Handshake{{Version: "1.162.0"}, {Version: "unknown"}, {}} {
		if err := CheckServable(old, hs); err != nil {
			t.Errorf("%+v was refused an entry every handshaking build has: %v", hs, err)
		}
	}
}

// Every baseline release parses, so a bad number cannot silently grant or
// refuse an entry.
func TestBaselineReleasesParse(t *testing.T) {
	for c, release := range baselineCapabilities {
		if release == "" {
			continue
		}
		if _, ok := parseRelease(release); !ok {
			t.Errorf("%s: release %q is not MAJOR.MINOR.PATCH", c, release)
		}
	}
}

// A value omitempty drops is absent on the wire, so an empty list needs no
// entry: the sidecar never sees the key. The listener form sends one for a
// list switched on with nothing in it.
func TestAnEmptyListIsNotASetField(t *testing.T) {
	cfg := httpLaneWithSensitiveParams()
	cfg.Listeners[0].HTTP.SensitiveQueryParams = []string{}
	for _, hs := range []Handshake{{Version: "1.184.1"}, {Capabilities: []string{"rule:operation", "protocol:http"}}} {
		if err := CheckServable(cfg, hs); err != nil {
			t.Errorf("%+v was refused an empty list: %v", hs, err)
		}
	}
}

// These keys shipped without a cap tag, so the plane served them to builds
// that refuse the whole document over them (EVL-338). Each is now refused
// to a release before its own, naming that release.
func TestAKeyIsRefusedToAReleaseBeforeIt(t *testing.T) {
	for _, tc := range []struct {
		key, since, before string
		set                func(*Config)
	}{
		{"trust", "1.198.0", "1.197.0", func(c *Config) { c.Trust = &TrustConfig{CAFile: "/ca.pem"} }},
		{"google_identity", "1.198.0", "1.197.0", func(c *Config) {
			c.Listeners[0].GoogleIdentity = &GoogleIdentityConfig{TokenInfoURL: "https://t"}
		}},
		{"mcp", "1.199.0", "1.198.1", func(c *Config) { c.MCP = &MCPConfig{Listen: "127.0.0.1:8765"} }},
		{"postgres", "1.201.0", "1.200.0", func(c *Config) { c.Listeners[0].Postgres = &PostgresConfig{} }},
		{"ssh.relay", "1.207.0", "1.206.0", func(c *Config) {
			c.Listeners[0].Protocol = "ssh"
			c.Listeners[0].SSH = &SSHConfig{Relay: &SSHRelayConfig{}}
		}},
	} {
		cfg := *pgLane()
		tc.set(&cfg)
		for _, hs := range []Handshake{{Version: tc.since}, {Capabilities: SidecarCapabilities()}} {
			if err := CheckServable(cfg, hs); err != nil {
				t.Errorf("%s: %+v was refused a key it decodes: %v", tc.key, hs, err)
			}
		}
		err := CheckServable(cfg, Handshake{Version: tc.before})
		if err == nil || !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), tc.since+" or later") {
			t.Errorf("%s: %s was served the key, or the refusal does not name %s: %v", tc.key, tc.before, tc.since, err)
		}
	}
}
