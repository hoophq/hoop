package services

import (
	"encoding/json"
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
