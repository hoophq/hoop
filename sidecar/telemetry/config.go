package telemetry

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Config is the top-level "telemetry" block. Absent means off: no SDK is
// built and every data-path hook stays nil. Present, it pushes OTLP to one
// endpoint; metrics are on with the block, traces and events opt in.
//
// Every field omits its zero value and the block carries cap:"telemetry"
// on daemon.Config, because the control plane serves this document and a
// build from before the key refuses the whole file over it (ADR-0022).
type Config struct {
	// OTLP names the collector and how to reach it. Required.
	OTLP OTLPConfig `json:"otlp,omitzero"`

	// Resource adds attributes to every signal: deployment.environment,
	// a team label. service.name here overrides ServiceName.
	Resource map[string]string `json:"resource,omitempty"`

	// Metrics tunes the metrics signal. Nil is on with the defaults.
	Metrics *MetricsConfig `json:"metrics,omitempty"`

	// Traces turns on spans. Nil, or a zero sample_ratio, is off.
	Traces *TracesConfig `json:"traces,omitempty"`

	// Events turns on audit events as log records. Nil is off.
	Events *EventsConfig `json:"events,omitempty"`
}

// OTLPConfig is the exporter's transport.
type OTLPConfig struct {
	// Endpoint is host:port for grpc, or a URL with scheme for http.
	// Standard collector ports are 4317 (grpc) and 4318 (http).
	Endpoint string `json:"endpoint,omitempty"`

	// Protocol is grpc (default) or http, the two OTLP transports. http
	// is protobuf over HTTP/1.1 POSTs, for a path that blocks HTTP/2.
	Protocol string `json:"protocol,omitempty"`

	// Headers go on every export request: an authorization header for a
	// vendor endpoint.
	Headers map[string]string `json:"headers,omitempty"`

	// Insecure sends in the clear, for a collector in the same pod.
	Insecure bool `json:"insecure,omitempty"`

	// CAFile pins the collector's CA. Empty is the host trust store.
	CAFile string `json:"ca_file,omitempty"`

	// TimeoutSec bounds one export. Default 10.
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// MetricsConfig tunes the metrics signal.
type MetricsConfig struct {
	// Enabled turns metrics off when false. Nil is on.
	Enabled *bool `json:"enabled,omitempty"`
	// IntervalSec is how often the SDK collects and exports. Default 60.
	IntervalSec int `json:"interval_sec,omitempty"`
}

// TracesConfig tunes the traces signal.
type TracesConfig struct {
	// SampleRatio is the share of exchanges traced, 0 to 1. Decided once
	// per exchange, so every statement of a sampled connection is in.
	SampleRatio float64 `json:"sample_ratio,omitempty"`
}

// EventsConfig tunes audit events as log records.
type EventsConfig struct {
	// Enabled turns the signal on.
	Enabled bool `json:"enabled,omitempty"`
	// StatementText sends the statement text and HTTP bodies in the clear.
	// Off, the record carries a fingerprint, the same as audit
	// redact_statements. Off by default because the collector is a new
	// place audit data leaves the process.
	StatementText bool `json:"statement_text,omitempty"`
	// QueueSize bounds the records held for export; the oldest go first
	// when it is full. Default 4096.
	QueueSize int `json:"queue_size,omitempty"`
}

// OTLP transports.
const (
	ProtocolGRPC = "grpc"
	ProtocolHTTP = "http"
)

// Defaults.
const (
	DefaultTimeoutSec  = 10
	DefaultIntervalSec = 60
	DefaultQueueSize   = 4096
)

// Validate reports every problem in the block, each prefixed "telemetry:".
// A nil receiver is the absent block and has none.
func (c *Config) Validate() []string {
	if c == nil {
		return nil
	}
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, "telemetry: "+fmt.Sprintf(format, args...))
	}

	switch proto := c.OTLP.Protocol; proto {
	case "", ProtocolGRPC:
		if c.OTLP.Endpoint == "" {
			add(`otlp.endpoint is required; remove the block to turn telemetry off`)
		} else if _, _, err := net.SplitHostPort(c.OTLP.Endpoint); err != nil {
			add("otlp.endpoint %q: a grpc endpoint is host:port, such as otel-collector:4317", c.OTLP.Endpoint)
		}
	case ProtocolHTTP:
		if c.OTLP.Endpoint == "" {
			add(`otlp.endpoint is required; remove the block to turn telemetry off`)
		} else if u, err := url.Parse(c.OTLP.Endpoint); err != nil || u.Host == "" ||
			(u.Scheme != "http" && u.Scheme != "https") {
			add("otlp.endpoint %q: an http endpoint is a URL, such as http://otel-collector:4318", c.OTLP.Endpoint)
		}
	default:
		add("otlp.protocol %q is not one of grpc, http", proto)
	}
	if c.OTLP.TimeoutSec < 0 {
		add("otlp.timeout_sec must not be negative")
	}
	for k := range c.OTLP.Headers {
		if strings.TrimSpace(k) == "" {
			add("otlp.headers has an empty name")
			break
		}
	}
	if c.Metrics != nil && c.Metrics.IntervalSec < 0 {
		add("metrics.interval_sec must not be negative")
	}
	if c.Traces != nil && (c.Traces.SampleRatio < 0 || c.Traces.SampleRatio > 1) {
		add("traces.sample_ratio %v is outside 0..1", c.Traces.SampleRatio)
	}
	if c.Events != nil && c.Events.QueueSize < 0 {
		add("events.queue_size must not be negative")
	}
	if !c.MetricsEnabled() && !c.TracesEnabled() && !c.EventsEnabled() {
		add("every signal is off; remove the block or turn one on")
	}
	return problems
}

// MetricsEnabled is true unless metrics.enabled is false.
func (c *Config) MetricsEnabled() bool {
	return c != nil && (c.Metrics == nil || c.Metrics.Enabled == nil || *c.Metrics.Enabled)
}

// TracesEnabled is true when a positive sample ratio is set.
func (c *Config) TracesEnabled() bool {
	return c != nil && c.Traces != nil && c.Traces.SampleRatio > 0
}

// EventsEnabled is true when the events block is on.
func (c *Config) EventsEnabled() bool {
	return c != nil && c.Events != nil && c.Events.Enabled
}

// Protocol is the transport with the default applied.
func (c *Config) Protocol() string {
	if c.OTLP.Protocol == "" {
		return ProtocolGRPC
	}
	return c.OTLP.Protocol
}

// TimeoutSec, IntervalSec and QueueSize apply the defaults.
func (c *Config) TimeoutSec() int {
	if c.OTLP.TimeoutSec > 0 {
		return c.OTLP.TimeoutSec
	}
	return DefaultTimeoutSec
}

func (c *Config) IntervalSec() int {
	if c.Metrics != nil && c.Metrics.IntervalSec > 0 {
		return c.Metrics.IntervalSec
	}
	return DefaultIntervalSec
}

func (c *Config) QueueSize() int {
	if c.Events != nil && c.Events.QueueSize > 0 {
		return c.Events.QueueSize
	}
	return DefaultQueueSize
}
