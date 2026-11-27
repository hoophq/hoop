package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseSidecarConfiguration(t *testing.T) {
	for _, tt := range []string{"", "{}"} {
		cfg, err := ParseSidecarConfiguration(json.RawMessage(tt))
		if err != nil {
			t.Fatalf("raw=%q: unexpected error: %v", tt, err)
		}
		if len(cfg.Listeners) != 0 {
			t.Errorf("raw=%q: want no listeners, got %d", tt, len(cfg.Listeners))
		}
		if cfg.LogLevel != "" || cfg.Audit.File != "" || cfg.Admin.Listen != "" {
			t.Errorf("raw=%q: gateway must not inject defaults, got log_level=%q audit.file=%q admin.listen=%q",
				tt, cfg.LogLevel, cfg.Audit.File, cfg.Admin.Listen)
		}
	}

	raw := `{"log_level":"debug","audit":{"file":"-"},"listeners":[{"name":"pg","protocol":"postgres","listen":"0.0.0.0:5432","upstream":"db:5432"}]}`
	cfg, err := ParseSidecarConfiguration(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("want log_level=debug, got %q", cfg.LogLevel)
	}
	if cfg.Audit.File != "-" {
		t.Errorf("want audit.file=-, got %q", cfg.Audit.File)
	}
	if len(cfg.Listeners) != 1 {
		t.Fatalf("want one listener, got %d", len(cfg.Listeners))
	}
	l := cfg.Listeners[0]
	if l.Name != "pg" || l.Protocol != "postgres" || l.Listen != "0.0.0.0:5432" || l.Upstream != "db:5432" {
		t.Errorf("listener not decoded: %+v", l)
	}

	if _, err := ParseSidecarConfiguration(json.RawMessage(`null`)); err == nil {
		t.Error("want error for an explicit null document, got nil")
	}

	if _, err := ParseSidecarConfiguration(json.RawMessage(`{"listners":[]}`)); err == nil {
		t.Error("want error for unknown field, got nil")
	}
}

func TestParseSidecarConfigurationCanonicalizesLoadFromDisk(t *testing.T) {
	off, err := ParseSidecarConfiguration(json.RawMessage(`{"load_from_disk":false}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if off.LoadFromDisk != nil {
		t.Errorf("load_from_disk false was stored as %v; want it dropped", *off.LoadFromDisk)
	}

	on, err := ParseSidecarConfiguration(json.RawMessage(`{"load_from_disk":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if on.LoadFromDisk == nil || !*on.LoadFromDisk {
		t.Errorf("load_from_disk true was not preserved: %+v", on.LoadFromDisk)
	}
}

func TestParseSidecarConfigurationPatch(t *testing.T) {
	// A partial patch returns only the keys the caller sent, so the stored
	// document keeps every field the patch does not name.
	merge, remove, err := ParseSidecarConfigurationPatch(json.RawMessage(`{"log_level":"debug"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if remove {
		t.Error("a patch without load_from_disk must not remove it")
	}
	if got := string(merge); got != `{"log_level":"debug"}` {
		t.Errorf("merge = %s; want only the sent key", got)
	}

	// load_from_disk true stays in the merge so the plane serves the flag.
	merge, remove, err = ParseSidecarConfigurationPatch(json.RawMessage(`{"load_from_disk":true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if remove {
		t.Error("load_from_disk true must be merged, not removed")
	}
	if got := string(merge); got != `{"load_from_disk":true}` {
		t.Errorf("merge = %s; want the flag merged", got)
	}

	// load_from_disk false is dropped from the merge and removed from the
	// stored document: an older sidecar rejects an unknown key.
	merge, remove, err = ParseSidecarConfigurationPatch(json.RawMessage(`{"load_from_disk":false}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !remove {
		t.Error("load_from_disk false must remove the key")
	}
	if got := string(merge); got != `{}` {
		t.Errorf("merge = %s; want the flag dropped", got)
	}

	if _, _, err := ParseSidecarConfigurationPatch(json.RawMessage(`null`)); err == nil {
		t.Error("want error for an explicit null document, got nil")
	}
	if _, _, err := ParseSidecarConfigurationPatch(json.RawMessage(`{"listners":[]}`)); err == nil {
		t.Error("want error for unknown field, got nil")
	}
}

// The control plane stores a listener analyzer block that names an approval
// rule. DisallowUnknownFields is what makes this a real check: before the
// field existed the write was refused, and the rule EVL-287 authorizes a
// review against had nowhere to live.
func TestParseSidecarConfigurationKeepsTheApprovalRule(t *testing.T) {
	raw := `{"listeners":[{"name":"pg","protocol":"postgres","listen":"0.0.0.0:5432",` +
		`"upstream":"db:5432","analyzer":{"high":"require_review",` +
		`"approval_rule":"payments-approvers"}}]}`
	cfg, err := ParseSidecarConfiguration(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("a config naming an approval rule was refused: %v", err)
	}
	if len(cfg.Listeners) != 1 || cfg.Listeners[0].Analyzer == nil {
		t.Fatalf("the analyzer block did not decode: %+v", cfg.Listeners)
	}
	if got := cfg.Listeners[0].Analyzer.ApprovalRule; got != "payments-approvers" {
		t.Errorf("approval_rule = %q; want payments-approvers", got)
	}
}

// TestValidateListenerNames pins the whole refusal table. The rule exists
// because a listener name is the only handle anything outside the document
// has on a lane: an approval rule is authorized through it
// (gateway/api/sidecar/reviews.go listenerNamesApprovalRule) and the sidecar's
// reload path keys its per-lane rule documents and running servers by it
// (sidecar/daemon/reload.go). The daemon itself requires neither presence nor
// uniqueness, so nothing else enforces this.
func TestValidateListenerNames(t *testing.T) {
	for _, tt := range []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"no listeners at all", `{}`, false},
		{"one named listener", `{"listeners":[{"name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432"}]}`, false},
		{
			"two listeners with distinct names",
			`{"listeners":[{"name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432"},` +
				`{"name":"api","protocol":"http","listen":":8080","upstream":"api:80"}]}`,
			false,
		},
		{
			// The daemon keys uniqueness on network|listen, so this document
			// is valid to it and saved with a 200 before this guard.
			"two listeners sharing a name on different ports",
			`{"listeners":[{"name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432"},` +
				`{"name":"pg","protocol":"postgres","listen":":5433","upstream":"db2:5432"}]}`,
			true,
		},
		{
			// daemon.go displayName falls back to listener[i], which nothing
			// outside the document can name.
			"a listener with no name",
			`{"listeners":[{"protocol":"postgres","listen":":5432","upstream":"db:5432"}]}`,
			true,
		},
		{
			"a listener named only with spaces",
			`{"listeners":[{"name":"   ","protocol":"postgres","listen":":5432","upstream":"db:5432"}]}`,
			true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSidecarConfiguration(json.RawMessage(tt.raw))
			if tt.wantErr && err == nil {
				t.Fatal("want a refusal, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("want the document accepted, got %v", err)
			}
		})
	}
}

// TestParseSidecarConfigurationPatchChecksListenerNamesOnlyWhenSent proves the
// patch path does not refuse a document it never received. A patch that leaves
// listeners alone decodes into a probe with none, which must not read as "a
// nameless listener".
func TestParseSidecarConfigurationPatchChecksListenerNamesOnlyWhenSent(t *testing.T) {
	if _, _, err := ParseSidecarConfigurationPatch(json.RawMessage(`{"log_level":"debug"}`)); err != nil {
		t.Fatalf("a patch that does not name listeners must be accepted, got %v", err)
	}

	dup := `{"listeners":[{"name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432"},` +
		`{"name":"pg","protocol":"postgres","listen":":5433","upstream":"db2:5432"}]}`
	if _, _, err := ParseSidecarConfigurationPatch(json.RawMessage(dup)); err == nil {
		t.Error("a patch that sends duplicate listener names must be refused, got nil")
	}
}

// TestCheckSidecarConfigurationLimits pins the gateway half of the free-tier
// caps. The numbers themselves belong to sidecar/daemon/limits.go; what this
// asserts is that the gateway asks the same question the sidecar asks at boot,
// so a document the control plane stores is a document a sidecar starts on.
func TestCheckSidecarConfigurationLimits(t *testing.T) {
	oneRule := `{"listeners":[{"name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432"}],` +
		`"guardrails":{"rules":[{"name":"no-drop","type":"deny_words_list","words":["DROP TABLE"]}]}}`
	cfg, err := ParseSidecarConfiguration(json.RawMessage(oneRule))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if err := CheckSidecarConfigurationLimits(cfg, nil); err != nil {
		t.Fatalf("one guardrail rule is inside the free tier, got %v", err)
	}

	// Two guardrail rules in one document: one on the default block and one
	// on the lane. The daemon counts what a document AUTHORS across every
	// block, so this is two even though each site holds one.
	twoRules := `{"listeners":[{"name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432",` +
		`"guardrails":{"rules":[{"name":"lane","type":"deny_words_list","words":["TRUNCATE"]}]}}],` +
		`"guardrails":{"rules":[{"name":"no-drop","type":"deny_words_list","words":["DROP TABLE"]}]}}`
	cfg, err = ParseSidecarConfiguration(json.RawMessage(twoRules))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	err = CheckSidecarConfigurationLimits(cfg, nil)
	if err == nil {
		t.Fatal("two guardrail rules exceed the free tier; want a refusal, got nil")
	}
	var overCap ErrSidecarConfigOverCap
	if !errors.As(err, &overCap) {
		t.Fatalf("want ErrSidecarConfigOverCap so the handler can answer 422, got %T", err)
	}
	// The message is the daemon's own, naming each block to merge. Without
	// the breakdown an admin reads a total and starts from the top of the
	// file.
	if !strings.Contains(overCap.Error(), "guardrails") || !strings.Contains(overCap.Error(), "pg") {
		t.Errorf("want the per-site breakdown naming both blocks, got %q", overCap.Error())
	}

	// A malformed licence must not read as a licensed one. license.Load
	// answers an invalid Status, which grants nothing, so the caps hold.
	if err := CheckSidecarConfigurationLimits(cfg, json.RawMessage(`{"not":"a license"}`)); err == nil {
		t.Error("an unverifiable licence must not lift the caps")
	}
}

// A rule written inside the sidecar's own configuration, on a lane or in its
// rule_library, gets the sidecar's refusal at the save: a resource pattern
// that matches nothing would load, deny nothing, and let the call it names
// reach the upstream. A pattern a request can reach still saves.
func TestCheckSidecarConfigurationRefusesAnInertResourcePattern(t *testing.T) {
	const rule = `{"name":"deny-upload","type":"http_resource","resources":["%s"]}`
	for _, tc := range []struct {
		protocol, bad string
		good          []string
	}{
		{"grpc", "/repro.v1.*/Upload", []string{"/repro.v1.Alpha/*", "/**"}},
		{"http", "/api/v1*/users", []string{"/api/v1/**", "/reports/*.pdf", "/admin/../public", "/api/users?limit=10"}},
	} {
		for site, doc := range map[string]func(string) string{
			"lane": func(p string) string {
				return `{"listeners":[{"name":"api","protocol":"` + tc.protocol +
					`","listen":":8443","upstream":"svc:443","guardrails":{"rules":[` + fmt.Sprintf(rule, p) + `]}}]}`
			},
			"rule_library": func(p string) string {
				return `{"rule_library":{"guardrails":[` + fmt.Sprintf(rule, p) + `]},` +
					`"listeners":[{"name":"api","protocol":"` + tc.protocol + `","listen":":8443","upstream":"svc:443"}]}`
			},
		} {
			name := tc.protocol + "/" + site
			cfg, err := ParseSidecarConfiguration(json.RawMessage(doc(tc.bad)))
			if err != nil {
				t.Fatalf("%s: unexpected parse error: %v", name, err)
			}
			err = CheckSidecarConfiguration(cfg)
			var invalid ErrSidecarConfigInvalid
			if !errors.As(err, &invalid) {
				t.Fatalf("%s: want ErrSidecarConfigInvalid so the handler answers 422, got %v", name, err)
			}
			want := `deny-upload: resource "` + tc.bad + `" matches no request as written`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal = %v, want it to contain %q", name, err, want)
			}

			for _, good := range tc.good {
				cfg, err = ParseSidecarConfiguration(json.RawMessage(doc(good)))
				if err != nil {
					t.Fatalf("%s: unexpected parse error: %v", name, err)
				}
				if err := CheckSidecarConfiguration(cfg); err != nil {
					t.Errorf("%s: %s was refused: %v", name, good, err)
				}
			}
		}
	}
}
