package otlp

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/telemetry"
)

func testResource() telemetry.Resource {
	return telemetry.Resource{Version: "test", InstanceID: "inst-1", Entrypoint: "hoop-inspect", Runtime: "linux"}
}

func httpConfig(endpoint string) *telemetry.Config {
	return &telemetry.Config{
		OTLP:    telemetry.OTLPConfig{Endpoint: endpoint, Protocol: telemetry.ProtocolHTTP, TimeoutSec: 2},
		Metrics: &telemetry.MetricsConfig{IntervalSec: 1},
	}
}

func mustNew(t *testing.T, cfg *telemetry.Config) telemetry.Exporter {
	t.Helper()
	if problems := cfg.Validate(); len(problems) > 0 {
		t.Fatalf("config: %v", problems)
	}
	exp, err := New(cfg, testResource())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return exp
}

func shutdown(t *testing.T, exp telemetry.Exporter) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exp.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestMetricsReachTheCollector(t *testing.T) {
	rcv := newReceiver(t)
	exp := mustNew(t, httpConfig(rcv.srv.URL))
	exp.ObserveProcess(func() telemetry.ProcessSnapshot {
		return telemetry.ProcessSnapshot{
			Lanes:            []telemetry.LaneStats{{Lane: "pg-prod", Protocol: "postgres", Active: 2, Total: 7}},
			ConfigGeneration: 3,
		}
	})
	lane := exp.Lane("pg-prod", "postgres")

	ctx := context.Background()
	stmt := inspect.Statement{Protocol: inspect.Postgres, Direction: inspect.FromClient, Operation: inspect.OpDelete, Text: "DELETE FROM users"}
	_, end := lane.BeginStatement(ctx, stmt)
	end(telemetry.Verdict{Denied: true, Source: "rules", Rule: "no-delete"})
	lane.Upstream(ctx, 5*time.Millisecond, telemetry.PairingExact)
	lane.Masked(3)
	exp.AnalyzerCall(telemetry.AnalyzerCall{Analyzer: "pg-prod", Provider: "openai", Model: "gpt", Outcome: "ok", Duration: 300 * time.Millisecond, Attempts: 2})
	shutdown(t, exp)

	res := rcv.resourceAttrs()
	if res["service.name"] != telemetry.ServiceName {
		t.Errorf("service.name = %q, want %q", res["service.name"], telemetry.ServiceName)
	}
	if res["hoop.schema.version"] != telemetry.SchemaVersion {
		t.Errorf("hoop.schema.version = %q, want %q", res["hoop.schema.version"], telemetry.SchemaVersion)
	}
	if res["service.instance.id"] != "inst-1" || res["hoop.sidecar.entrypoint"] != "hoop-inspect" {
		t.Errorf("resource = %v", res)
	}

	var denied *point
	for _, p := range rcv.metric("hoop.sidecar.statements") {
		if p.attrs["hoop.verdict"] == "deny" && p.attrs["hoop.deny.source"] == "rules" {
			denied = &p
		}
	}
	if denied == nil {
		t.Fatalf("no deny/rules point in %v", rcv.metric("hoop.sidecar.statements"))
	}
	if denied.value != 1 || denied.attrs["hoop.lane"] != "pg-prod" || denied.attrs["hoop.operation"] != "delete" || denied.attrs["hoop.direction"] != "client" {
		t.Errorf("deny point = %+v", *denied)
	}

	var upstream bool
	for _, p := range rcv.metric("hoop.sidecar.statement.duration") {
		if p.attrs["hoop.stage"] == "upstream" && p.attrs["hoop.pairing"] == "exact" && p.value == 1 {
			upstream = true
		}
	}
	if !upstream {
		t.Errorf("no upstream/exact histogram point in %v", rcv.metric("hoop.sidecar.statement.duration"))
	}
	if ps := rcv.metric("hoop.sidecar.masked_values"); len(ps) != 1 || ps[0].value != 3 {
		t.Errorf("masked_values = %v", ps)
	}
	if ps := rcv.metric("hoop.sidecar.analyzer.retries"); len(ps) != 1 || ps[0].value != 1 || ps[0].attrs["hoop.provider"] != "openai" {
		t.Errorf("analyzer.retries = %v", ps)
	}
	if ps := rcv.metric("hoop.sidecar.connections.active"); len(ps) != 1 || ps[0].value != 2 || ps[0].attrs["hoop.protocol"] != "postgres" {
		t.Errorf("connections.active = %v", ps)
	}
	if ps := rcv.metric("hoop.sidecar.config.generation"); len(ps) != 1 || ps[0].value != 3 {
		t.Errorf("config.generation = %v", ps)
	}

	keys := rcv.metricAttributeKeys()
	for _, forbidden := range []string{"hoop.principal", "hoop.statement", "hoop.session_id"} {
		if keys[forbidden] {
			t.Errorf("metric attribute %q must never be exported", forbidden)
		}
	}
}

func logAttrs(r *logspb.LogRecord) map[string]string { return attrMap(r.Attributes) }

func TestEventsAreRedactedByDefault(t *testing.T) {
	violation := audit.Event{
		Kind:       audit.KindViolation,
		Timestamp:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		SessionID:  "sess-1",
		Principal:  "alice",
		Protocol:   inspect.Postgres,
		Connection: "pg-prod",
		Operation:  inspect.OpDelete,
		Statement:  "DELETE FROM users",
		Tables:     []string{"users"},
		Rule:       "no-delete",
		Direction:  inspect.FromClient,
		Metadata:   map[string]string{"review_id": "rev-9", "deployment": "prod"},
	}

	write := func(t *testing.T, statementText bool) *logspb.LogRecord {
		t.Helper()
		rcv := newReceiver(t)
		cfg := httpConfig(rcv.srv.URL)
		cfg.Events = &telemetry.EventsConfig{Enabled: true, StatementText: statementText}
		exp := mustNew(t, cfg)
		sink := exp.AuditSink(audit.SinkOptions{})
		if sink == nil {
			t.Fatal("AuditSink returned nil with events on")
		}
		if err := sink.Write(context.Background(), violation); err != nil {
			t.Fatalf("Write: %v", err)
		}
		shutdown(t, exp)
		recs := rcv.logRecords()
		if len(recs) != 1 {
			t.Fatalf("got %d records, want 1", len(recs))
		}
		return recs[0]
	}

	rec := write(t, false)
	if body := anyString(rec.Body); body != "violation" {
		t.Errorf("body = %q, want violation", body)
	}
	if rec.SeverityNumber != logspb.SeverityNumber_SEVERITY_NUMBER_WARN {
		t.Errorf("severity = %v, want WARN", rec.SeverityNumber)
	}
	if got := time.Unix(0, int64(rec.TimeUnixNano)).UTC(); !got.Equal(violation.Timestamp) {
		t.Errorf("timestamp = %v, want %v", got, violation.Timestamp)
	}
	attrs := logAttrs(rec)
	if !strings.HasPrefix(attrs["hoop.statement"], "sha256:") {
		t.Errorf("hoop.statement = %q, want a fingerprint", attrs["hoop.statement"])
	}
	if attrs["hoop.verdict"] != "deny" {
		t.Errorf("hoop.verdict = %q, want deny", attrs["hoop.verdict"])
	}
	if attrs["hoop.principal"] != "alice" || attrs["hoop.session_id"] != "sess-1" || attrs["hoop.lane"] != "pg-prod" {
		t.Errorf("identity attributes = %v", attrs)
	}
	if attrs["hoop.review.id"] != "rev-9" || attrs["hoop.meta.deployment"] != "prod" || attrs["hoop.tables"] != "users" {
		t.Errorf("detail attributes = %v", attrs)
	}

	rec = write(t, true)
	if got := logAttrs(rec)["hoop.statement"]; got != "DELETE FROM users" {
		t.Errorf("hoop.statement with statement_text = %q, want the text", got)
	}
}

func TestEventsOffReturnsNoSink(t *testing.T) {
	rcv := newReceiver(t)
	exp := mustNew(t, httpConfig(rcv.srv.URL))
	defer shutdown(t, exp)
	if exp.AuditSink(audit.SinkOptions{}) != nil {
		t.Error("AuditSink must be nil with events off")
	}
}

func TestEventSinkNeverBlocksOrFails(t *testing.T) {
	release := make(chan struct{})
	srv := newBlockingServer(t, release)
	defer close(release)

	cfg := httpConfig(srv.URL)
	cfg.OTLP.TimeoutSec = 1
	cfg.Events = &telemetry.EventsConfig{Enabled: true, QueueSize: 8}
	exp := mustNew(t, cfg)
	sink := exp.AuditSink(audit.SinkOptions{})

	ev := audit.Event{Kind: audit.KindStatement, Principal: "alice", Statement: "select 1", Allowed: true}
	start := time.Now()
	for i := range 10_000 {
		if err := sink.Write(context.Background(), ev); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("10000 writes took %v, want under 1s", d)
	}
	stats := exp.Stats()
	if stats.EventsDropped == 0 {
		t.Error("EventsDropped = 0, want drops with a stalled collector and queue_size 8")
	}
	if stats.EventsSent != 10_000 {
		t.Errorf("EventsSent = %d, want 10000", stats.EventsSent)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start = time.Now()
	_ = exp.Shutdown(ctx)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Shutdown took %v, want under 3s", d)
	}
}

// newBlockingServer answers no request until release is closed, the
// collector that accepted the connection and went silent.
func newBlockingServer(t *testing.T, release <-chan struct{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSpansFormAnExchangeTree(t *testing.T) {
	rcv := newReceiver(t)
	cfg := httpConfig(rcv.srv.URL)
	cfg.Traces = &telemetry.TracesConfig{SampleRatio: 1}
	exp := mustNew(t, cfg)
	lane := exp.Lane("api", "grpc")

	ctx, endExchange := lane.BeginExchange(context.Background(), telemetry.ExchangeRPC, telemetry.Attr{Key: "hoop.principal", Value: "alice"})
	stmt := inspect.Statement{Protocol: inspect.GRPC, Direction: inspect.FromClient, Operation: inspect.OpPost, Text: "secret payload",
		HTTP: &inspect.HTTPDetail{Method: "POST", Path: "/users.v1.Users/Delete", StatusCode: 200}}
	stmtCtx, endStatement := lane.BeginStatement(ctx, stmt)
	stageCtx, endStage := lane.BeginStage(stmtCtx, telemetry.StagePolicy, telemetry.EngineOPA)

	// An outbound call made under the stage span carries the trace.
	req, _ := http.NewRequestWithContext(stageCtx, http.MethodGet, rcv.srv.URL+"/echo", nil)
	resp, err := (&http.Client{Transport: HTTPTransport(nil)}).Do(req)
	if err != nil {
		t.Fatalf("echo: %v", err)
	}
	resp.Body.Close()
	transportHeader := rcv.echoedHeader("traceparent")

	injected := http.Header{}
	exp.Inject(stageCtx, injected)
	untraced := http.Header{}
	exp.Inject(context.Background(), untraced)

	endStage(telemetry.OutcomeDeny, nil)
	endStatement(telemetry.Verdict{Denied: true, Source: "opa", Rule: "deny-delete"})
	endExchange(nil)
	shutdown(t, exp)

	spans := rcv.spans()
	if len(spans) != 3 {
		t.Fatalf("got %d spans, want 3: %v", len(spans), spanNames(spans))
	}
	byName := map[string]*tracepb.Span{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	rpc, statement, policy := byName["hoop.rpc"], byName["hoop.statement"], byName["policy"]
	if rpc == nil || statement == nil || policy == nil {
		t.Fatalf("span names = %v", spanNames(spans))
	}
	if len(rpc.ParentSpanId) != 0 {
		t.Errorf("hoop.rpc has a parent %x, want root", rpc.ParentSpanId)
	}
	if string(statement.ParentSpanId) != string(rpc.SpanId) {
		t.Errorf("statement parent = %x, want rpc %x", statement.ParentSpanId, rpc.SpanId)
	}
	if string(policy.ParentSpanId) != string(statement.SpanId) {
		t.Errorf("policy parent = %x, want statement %x", policy.ParentSpanId, statement.SpanId)
	}
	if rpc.Kind != tracepb.Span_SPAN_KIND_SERVER {
		t.Errorf("rpc kind = %v, want SERVER", rpc.Kind)
	}

	sa := attrMap(statement.Attributes)
	if sa["hoop.verdict"] != "deny" || sa["hoop.deny.source"] != "opa" || sa["hoop.rule"] != "deny-delete" {
		t.Errorf("statement attributes = %v", sa)
	}
	if sa["hoop.http.method"] != "POST" || sa["hoop.http.path"] != "/users.v1.Users/Delete" || sa["hoop.http.status_code"] != "200" {
		t.Errorf("statement http attributes = %v", sa)
	}
	if statement.Status.GetCode() != tracepb.Status_STATUS_CODE_ERROR || statement.Status.GetMessage() != "deny-delete" {
		t.Errorf("statement status = %v", statement.Status)
	}
	pa := attrMap(policy.Attributes)
	if pa["hoop.engine"] != "opa" || pa["hoop.outcome"] != "deny" {
		t.Errorf("policy attributes = %v", pa)
	}
	if ra := attrMap(rpc.Attributes); ra["hoop.principal"] != "alice" || ra["hoop.lane"] != "api" {
		t.Errorf("rpc attributes = %v", ra)
	}
	for _, s := range spans {
		for _, kv := range s.Attributes {
			if kv.Key == "hoop.statement" {
				t.Errorf("span %s carries hoop.statement", s.Name)
			}
		}
	}

	wantTrace := hex.EncodeToString(policy.TraceId)
	wantSpan := hex.EncodeToString(policy.SpanId)
	for name, h := range map[string]string{"HTTPTransport": transportHeader, "Inject": injected.Get("traceparent")} {
		if !strings.Contains(h, wantTrace) || !strings.Contains(h, wantSpan) {
			t.Errorf("%s traceparent = %q, want trace %s span %s", name, h, wantTrace, wantSpan)
		}
	}
	if len(untraced) != 0 {
		t.Errorf("Inject without a span wrote %v", untraced)
	}
}

func spanNames(spans []*tracepb.Span) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name
	}
	return names
}

func TestTracesOffCostsNoSpan(t *testing.T) {
	rcv := newReceiver(t)
	exp := mustNew(t, httpConfig(rcv.srv.URL))
	defer shutdown(t, exp)
	lane := exp.Lane("pg", "postgres")

	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	stmt := inspect.Statement{Direction: inspect.FromClient, Operation: inspect.OpSelect, Text: "select 1"}
	got, end := lane.BeginStatement(ctx, stmt)
	if got != ctx {
		t.Error("BeginStatement with traces off must return the caller's context")
	}
	end(telemetry.Verdict{})
	ectx, endExchange := lane.BeginExchange(ctx, telemetry.ExchangeSession)
	if ectx != ctx {
		t.Error("BeginExchange with traces off must return the caller's context")
	}
	endExchange(nil)

	// Measured: 1 allocation, the EndStatement closure. The counter's
	// attribute set is cached after the first statement of a kind and the
	// SDK finds the aggregation by set hash, so the Add itself allocates
	// nothing.
	allocs := testing.AllocsPerRun(1000, func() {
		_, end := lane.BeginStatement(ctx, stmt)
		end(telemetry.Verdict{})
	})
	t.Logf("BeginStatement+EndStatement allocs: %v", allocs)
	if allocs > 2 {
		t.Errorf("BeginStatement+EndStatement allocates %v, want at most 2", allocs)
	}
}

type ctxKey struct{}
