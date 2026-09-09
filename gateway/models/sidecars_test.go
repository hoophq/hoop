package models

import "testing"

func TestSidecarConfigurationScan(t *testing.T) {
	var c SidecarConfiguration
	if err := c.Scan([]byte(`{"log_level":"debug","listeners":[{"name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432"}]}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.LogLevel != "debug" || len(c.Listeners) != 1 {
		t.Fatalf("document not decoded: %+v", c)
	}

	// A second row must not inherit the first one's fields.
	if err := c.Scan([]byte(`{"log_level":"info"}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.Listeners) != 0 {
		t.Errorf("listeners carried over from the previous scan: %+v", c.Listeners)
	}

	if err := c.Scan(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.LogLevel != "" {
		t.Errorf("want the zero config for a null column, got log_level=%q", c.LogLevel)
	}

	// A row this gateway cannot represent must fail loudly instead of being
	// served with the unknown control dropped.
	if err := c.Scan([]byte(`{"guadrails":{"mode":"deny"}}`)); err == nil {
		t.Error("want error for an unknown stored key, got nil")
	}
}
