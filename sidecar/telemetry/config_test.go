package telemetry

import (
	"strings"
	"testing"
)

// Validate refuses what the exporter could not build, with the field named,
// and accepts the two shapes an operator writes.
func TestValidateNamesTheField(t *testing.T) {
	no := func(cfg Config, want string) {
		t.Helper()
		problems := cfg.Validate()
		for _, p := range problems {
			if strings.Contains(p, want) {
				return
			}
		}
		t.Errorf("Validate() = %v, want a problem naming %q", problems, want)
	}
	ok := func(cfg Config) {
		t.Helper()
		if problems := cfg.Validate(); len(problems) > 0 {
			t.Errorf("Validate() = %v, want none", problems)
		}
	}

	ok(Config{OTLP: OTLPConfig{Endpoint: "otel-collector:4317"}})
	ok(Config{OTLP: OTLPConfig{Endpoint: "http://otel-collector:4318", Protocol: ProtocolHTTP}})

	no(Config{}, "otlp.endpoint is required")
	no(Config{OTLP: OTLPConfig{Endpoint: "http://x:4318"}}, "a grpc endpoint is host:port")
	no(Config{OTLP: OTLPConfig{Endpoint: "x:4318", Protocol: ProtocolHTTP}}, "an http endpoint is a URL")
	no(Config{OTLP: OTLPConfig{Endpoint: "x:4317", Protocol: "thrift"}}, "otlp.protocol")
	no(Config{OTLP: OTLPConfig{Endpoint: "x:4317"}, Traces: &TracesConfig{SampleRatio: 1.5}}, "traces.sample_ratio")
	no(Config{OTLP: OTLPConfig{Endpoint: "x:4317"}, Events: &EventsConfig{QueueSize: -1}}, "events.queue_size")
	off := false
	no(Config{OTLP: OTLPConfig{Endpoint: "x:4317"}, Metrics: &MetricsConfig{Enabled: &off}}, "every signal is off")

	var absent *Config
	if problems := absent.Validate(); problems != nil {
		t.Errorf("an absent block has problems: %v", problems)
	}
}

// Metrics are on with the block; traces and events opt in; defaults fill
// what the operator left out.
func TestSignalSwitchesAndDefaults(t *testing.T) {
	cfg := &Config{OTLP: OTLPConfig{Endpoint: "x:4317"}}
	if !cfg.MetricsEnabled() || cfg.TracesEnabled() || cfg.EventsEnabled() {
		t.Errorf("bare block: metrics=%v traces=%v events=%v", cfg.MetricsEnabled(), cfg.TracesEnabled(), cfg.EventsEnabled())
	}
	if cfg.Protocol() != ProtocolGRPC || cfg.TimeoutSec() != DefaultTimeoutSec ||
		cfg.IntervalSec() != DefaultIntervalSec || cfg.QueueSize() != DefaultQueueSize {
		t.Errorf("defaults not applied: %s %d %d %d", cfg.Protocol(), cfg.TimeoutSec(), cfg.IntervalSec(), cfg.QueueSize())
	}
	cfg.Traces = &TracesConfig{SampleRatio: 0}
	if cfg.TracesEnabled() {
		t.Error("a zero sample ratio turned traces on")
	}
	cfg.Traces.SampleRatio = 0.5
	cfg.Events = &EventsConfig{Enabled: true}
	if !cfg.TracesEnabled() || !cfg.EventsEnabled() {
		t.Error("traces and events did not turn on")
	}
}
