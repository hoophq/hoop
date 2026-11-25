package daemon

import (
	"slices"
	"strings"
	"testing"
)

func resourceCheckLane(protocol string) Config {
	return Config{
		Analyzer: &AnalyzerConfig{Provider: "stub", Model: "m"},
		Listeners: []ListenerConfig{{
			Name: "lane", Protocol: protocol, Listen: ":1", Upstream: "h:1",
			Analyzer: &LaneAnalyzerConfig{HighRisk: "block", ResourceCheck: true},
		}},
	}
}

// A lane whose codec has no check accepts the field: a plane that serves it
// to a mysql lane must not get the whole document refused.
func TestResourceCheckValidatesOnEveryLane(t *testing.T) {
	for _, p := range []string{"postgres", "mysql", "mssql", "http"} {
		cfg := resourceCheckLane(p)
		if err := cfg.Validate(); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestResourceCheckIsServedOnlyToABuildThatHasIt(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), "analyzer_resource_check") {
		t.Fatal("this build does not report analyzer_resource_check")
	}
	cfg := resourceCheckLane("postgres")
	if err := CheckServable(cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("refused to this build: %v", err)
	}
	old := slices.DeleteFunc(SidecarCapabilities(), func(c string) bool { return c == "analyzer_resource_check" })
	err := CheckServable(cfg, Handshake{Capabilities: old})
	if err == nil || !strings.Contains(err.Error(), "resource_check") {
		t.Errorf("served to a build without the capability: %v", err)
	}
}
