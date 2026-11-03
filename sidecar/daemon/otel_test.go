package daemon

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	telem "github.com/hoophq/hoop/sidecar/telemetry"
)

// A "telemetry" block needs the exporter linked. The config decoder accepts
// it in every build, because the control plane validates the same document
// with no exporter, so the refusal is Run's and Validate's, not
// Config.Validate's.
func TestATelemetryBlockNeedsALinkedExporter(t *testing.T) {
	cfg := &Config{Telemetry: &telem.Config{OTLP: telem.OTLPConfig{Endpoint: "otel-collector:4317"}}}

	withTelemetryExporter(t, nil)
	if err := checkTelemetry(cfg); err == nil || !strings.Contains(err.Error(), "no OpenTelemetry exporter") {
		t.Errorf("a build with no exporter accepted the block: %v", err)
	}

	withTelemetryExporter(t, func(*telem.Config, telem.Resource) (telem.Exporter, error) { return nil, nil })
	if err := checkTelemetry(cfg); err != nil {
		t.Errorf("a build with an exporter refused the block: %v", err)
	}

	if err := checkTelemetry(&Config{}); err != nil {
		t.Errorf("a config with no telemetry block was refused: %v", err)
	}
}

// Config.Validate carries the block's own problems, so -validate and the
// plane's save both report a bad endpoint before anything starts.
func TestConfigValidateReportsTelemetryProblems(t *testing.T) {
	cfg := Config{
		Listeners: []ListenerConfig{{Name: "appdb", Protocol: "postgres", Listen: ":15432", Upstream: "db:5432"}},
		Telemetry: &telem.Config{OTLP: telem.OTLPConfig{Endpoint: "http://x:4318"}},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "telemetry: otlp.endpoint") {
		t.Fatalf("Validate() = %v, want the telemetry endpoint problem", err)
	}
}

// Every outbound transport this process builds injects the trace context
// once an exporter is up, and stops when it closes, so a client built before
// Run (the OPA client, a provider's) still propagates.
func TestOutboundTransportsInjectWhileAnExporterIsUp(t *testing.T) {
	var seen http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	}))
	defer srv.Close()
	client := outboundHTTPClient(nil, "test")

	get := func() {
		t.Helper()
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	get()
	if seen.Get("traceparent") != "" {
		t.Fatal("a request with no exporter carried a traceparent")
	}

	outboundInject.Store(&injectFunc{func(_ context.Context, h http.Header) {
		h.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	}})
	t.Cleanup(func() { outboundInject.Store(nil) })
	get()
	if seen.Get("traceparent") == "" {
		t.Fatal("a request under a live exporter carried no traceparent")
	}

	outboundInject.Store(nil)
	get()
	if seen.Get("traceparent") != "" {
		t.Fatal("a request after the exporter closed still carried a traceparent")
	}
}

// The exporter's log sink joins the audit chain after the plane's copy, and a
// process without one builds the chain it always did.
func TestBuildAuditAppendsTheExporterSink(t *testing.T) {
	sink := &countingSink{}
	exp := &fakeExporter{sink: sink}
	o := &otelExporter{exp: exp}
	chain, err := buildAudit(AuditConfig{File: "/dev/null"}, nil, o, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := chain.sink.Write(context.Background(), audit.Event{Kind: audit.KindStatement}); err != nil {
		t.Fatal(err)
	}
	if sink.writes != 1 {
		t.Errorf("the exporter sink saw %d writes, want 1", sink.writes)
	}
	if exp.sinkOpts.RedactStatements {
		t.Error("the exporter sink was handed redaction the audit block did not set")
	}
}

type countingSink struct{ writes int }

func (s *countingSink) Write(context.Context, audit.Event) error { s.writes++; return nil }
func (s *countingSink) Close() error                             { return nil }

// fakeExporter satisfies telem.Exporter with the one method the audit
// chain reads.
type fakeExporter struct {
	telem.Exporter
	sink     audit.Sink
	sinkOpts audit.SinkOptions
}

func (f *fakeExporter) AuditSink(opts audit.SinkOptions) audit.Sink {
	f.sinkOpts = opts
	return f.sink
}

// withTelemetryExporter installs build for the test and restores the previous
// builder on cleanup.
func withTelemetryExporter(t *testing.T, build telem.Builder) {
	t.Helper()
	prev := NewTelemetryExporter
	t.Cleanup(func() { NewTelemetryExporter = prev })
	NewTelemetryExporter = build
}
