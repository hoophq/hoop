package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func slotsDoc() Config {
	return Config{Listeners: []ListenerConfig{{Name: "cell-a", Protocol: "ica", Listen: "0.0.0.0:21001", ICA: &ICAConfig{TenantID: "lab", SourceCIDRs: []string{"10.20.1.10/32"}, Slots: &ICASlotsConfig{FirstPort: 21001, Count: 2, UpstreamPattern: "prot-a%03d.lab.local", UpstreamPort: 2598, FrontendCIDR: "10.240.16.0/24", UpstreamCIDRs: []string{"10.77.0.0/24"}}}}}}
}
func TestSlotConfigAndCapabilities(t *testing.T) {
	cfg := slotsDoc()
	raw, _ := json.Marshal(cfg)
	if _, e := LoadConfigBytes(raw); e != nil {
		t.Fatal(e)
	}
	if e := CheckServable(cfg, Handshake{Capabilities: append(SidecarCapabilities(), "protocol:ica")}); e != nil {
		t.Fatal(e)
	}
	if e := CheckServable(cfg, Handshake{Capabilities: []string{"protocol:ica", "ica_source_cidrs"}}); e == nil || !strings.Contains(e.Error(), "ica_slots") {
		t.Fatal("old build accepted slots", e)
	}
	tests := []func(*Config){
		func(c *Config) { c.Listeners[0].Upstream = "10.77.0.31:2598" }, func(c *Config) { c.Listeners[0].ICA.EDT = true },
		func(c *Config) { c.Listeners[0].ICA.SourceCIDRs = nil }, func(c *Config) { c.Listeners[0].ICA.Slots.UpstreamPattern = "%s.local" },
		func(c *Config) { c.Listeners[0].ICA.Slots.FrontendCIDR = "10.77.0.0/24" }, func(c *Config) { c.Listeners[0].ICA.Slots.FirstPort = 19000; c.Listeners[0].Listen = ":19000" },
		func(c *Config) {
			c.Listeners = append(c.Listeners, ListenerConfig{Name: "other", Protocol: "http", Listen: "127.0.0.1:21002", Upstream: "127.0.0.1:80"})
		},
	}
	for i, mutate := range tests {
		c := slotsDoc()
		mutate(&c)
		raw, _ := json.Marshal(c)
		if _, e := LoadConfigBytes(raw); e == nil {
			t.Fatalf("invalid slot config %d accepted", i)
		}
	}
}
