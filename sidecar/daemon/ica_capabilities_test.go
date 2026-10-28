package daemon

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestICAMatchesOnlyRequiresItsCapability(t *testing.T) {
	const capability = "ica_pii_matches_only"
	current := SidecarCapabilities()
	if !slices.Contains(current, capability) {
		t.Fatal("this build does not advertise match-only configuration support")
	}
	older := slices.DeleteFunc(slices.Clone(current), func(c string) bool { return c == capability })
	for _, enabled := range []bool{true, false} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			cfg := Config{ICA: &ICARuntimeConfig{Options: &ICAOptions{PIIMatchesOnly: &enabled}}}
			if err := CheckServable(cfg, Handshake{Capabilities: current}); err != nil {
				t.Fatalf("a current build was refused: %v", err)
			}
			err := CheckServable(cfg, Handshake{Capabilities: older})
			if err == nil {
				t.Fatal("match-only configuration was served to a build that cannot decode it")
			}
			for _, want := range []string{"ica.options.pii_matches_only", capability} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not name %q", err, want)
				}
			}
		})
	}
	cfg := Config{ICA: &ICARuntimeConfig{Options: &ICAOptions{}}}
	if err := CheckServable(cfg, Handshake{Capabilities: older}); err != nil {
		t.Fatalf("an unset match-only option was refused to an older ICA build: %v", err)
	}
	raw, err := json.Marshal(ServedForm(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pii_matches_only") {
		t.Fatalf("an unset match-only option served its key: %s", raw)
	}
}
