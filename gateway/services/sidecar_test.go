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

	if _, err := ParseSidecarConfiguration(json.RawMessage(`{"listners":[]}`)); err == nil {
		t.Error("want error for unknown field, got nil")
	}
}
