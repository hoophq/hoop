package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// toolRiskProvider rates a call to delete_customer high and anything else
// low, and keeps what it was sent. err set fails every call.
type toolRiskProvider struct {
	mu      sync.Mutex
	prompts []string
	err     error
}

func (p *toolRiskProvider) Name() string { return "stub" }

func (p *toolRiskProvider) Classify(_ context.Context, _, content string) (*analyzer.Result, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, content)
	p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	if strings.Contains(content, "Tool: delete_customer") {
		return &analyzer.Result{RiskLevel: analyzer.RiskHigh, Title: "deletes a customer"}, nil
	}
	return &analyzer.Result{RiskLevel: analyzer.RiskLow, Title: "reads"}, nil
}

func (p *toolRiskProvider) sent() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.prompts)
}

// openMCPAnalyzerSession opens a session on an mcp lane named tools whose
// analyzer block is block. mcpGateway is the lane's mcp_gateway block, or "".
func openMCPAnalyzerSession(t *testing.T, block, mcpGateway string, p analyzer.Provider, cp *controlPlane, log *slog.Logger) (MCPLaneSession, *audit.MemorySink) {
	t.Helper()
	lc := `{"name":"tools","protocol":"mcp","listen":"127.0.0.1:18901","upstream":"https://mcp.example.com/mcp","analyzer":` + block
	if mcpGateway != "" {
		lc += `,"mcp_gateway":` + mcpGateway
	}
	cfg, err := LoadConfigBytes([]byte(`{"analyzer":{"provider":"stub","model":"m"},"listeners":[` + lc + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg.lic = licensed(t)
	lanes, err := buildLanes(cfg, nil, &analyzerDeps{cfg: cfg.Analyzer, provider: p, cp: cp})
	if err != nil {
		t.Fatal(err)
	}
	if log == nil {
		log = slog.Default()
	}
	sink := audit.NewMemorySink(64)
	ctx := context.Background()
	ls, err := openMCPLaneSession(ctx, lanes[0], newLiveRules(lanes[0]), sink, true, MCPCaller{Subject: "alice", MCPSessionID: "m1"}, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ls.Close(ctx) })
	return ls, sink
}

// A deny refuses the one call and the session goes on. The model reads the
// tool name and arguments, and each call's row carries the verdict.
func TestMCPLaneAnalyzerRefusesOneCall(t *testing.T) {
	p := &toolRiskProvider{}
	ls, sink := openMCPAnalyzerSession(t, `{"high":"block"}`, "", p, nil, nil)
	ctx := context.Background()
	if v := ls.ToolCall(ctx, "delete_customer", []byte(`{"id":"7"}`)); v.Allowed || v.Rule != "tools" || v.Message != "deletes a customer" {
		t.Fatalf("delete_customer verdict: %+v", v)
	}
	if v := ls.ToolCall(ctx, "echo", []byte(`{"message":"hi"}`)); !v.Allowed {
		t.Fatalf("the call after a deny was refused: %+v", v)
	}
	if sent := p.sent(); len(sent) != 2 || sent[0] != "Protocol: mcp\nTool: delete_customer\n\nArguments:\n{\"id\":\"7\"}" {
		t.Fatalf("the model was sent %q", sent)
	}
	rows := judged(sink)
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2: %+v", len(rows), rows)
	}
	for i, want := range []struct {
		kind                audit.Kind
		allowed             bool
		rule, level, action string
	}{
		{audit.KindViolation, false, "tools", "high", "block"},
		{audit.KindStatement, true, "", "low", "allow"},
	} {
		r := rows[i]
		if r.Kind != want.kind || r.Allowed != want.allowed || r.Rule != want.rule ||
			r.Metadata[policy.AnnotationRiskLevel] != want.level || r.Metadata[policy.AnnotationRiskAction] != want.action ||
			r.Metadata[analyzer.MetadataAIRule] != "tools" || r.Metadata[analyzer.MetadataAIStatus] != analyzer.StatusOK {
			t.Errorf("row %d: %+v, want %+v", i, r, want)
		}
	}
}

// ENG-967: a hold refuses the call and files nothing, with a plane that
// would accept the review. The session goes on.
func TestMCPLaneAnalyzerHoldRefusesTheCall(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusCreated, `{"forward":false,"review":{"id":"9f97","status":"REJECTED"}}`)
	ls, sink := openMCPAnalyzerSession(t, `{"high":"require_approval","approval_rule":"tool-approvers"}`, "", &toolRiskProvider{}, cp, nil)
	ctx := context.Background()
	if v := ls.ToolCall(ctx, "delete_customer", []byte(`{"id":"7"}`)); v.Allowed || v.Rule != "tools" ||
		!strings.Contains(v.Message, "an mcp lane does not hold a tool call for approval yet") {
		t.Fatalf("held call verdict: %+v", v)
	}
	if len(*calls) != 0 {
		t.Fatalf("the plane saw %d review requests, want none", len(*calls))
	}
	if v := ls.ToolCall(ctx, "echo", []byte(`{}`)); !v.Allowed {
		t.Fatalf("the call after a hold was refused: %+v", v)
	}
	if r := judged(sink)[0]; r.Allowed || r.Metadata[policy.AnnotationRiskAction] != string(analyzer.ActionRequireReview) {
		t.Fatalf("held row: %+v", r)
	}
}

// A provider failure behaves as on a postgres lane: fail_open allows the
// call and records ai_status error, fail_open false refuses it.
func TestMCPLaneAnalyzerProviderFailure(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		block   string
		allowed bool
	}{
		{`{"high":"block"}`, true},
		{`{"high":"block","fail_open":false}`, false},
	} {
		var logs bytes.Buffer
		p := &toolRiskProvider{err: errors.New("provider down")}
		ls, sink := openMCPAnalyzerSession(t, c.block, "", p, nil, slog.New(slog.NewTextHandler(&logs, nil)))
		v := ls.ToolCall(ctx, "delete_customer", []byte(`{"id":"7"}`))
		if v.Allowed != c.allowed || (!c.allowed && !strings.Contains(v.Message, "risk analysis unavailable")) {
			t.Errorf("%s: verdict %+v", c.block, v)
		}
		if r := judged(sink)[0]; r.Metadata[analyzer.MetadataAIStatus] != analyzer.StatusError {
			t.Errorf("%s: row %+v lacks ai_status error", c.block, r)
		}
		if !strings.Contains(logs.String(), "provider down") {
			t.Errorf("%s: the error is not logged: %s", c.block, logs.String())
		}

		cfg, err := LoadConfigBytes([]byte(`{"analyzer":{"provider":"stub","model":"m"},"listeners":[{"name":"appdb","protocol":"postgres","listen":"127.0.0.1:15432","upstream":"db:5432","analyzer":` + c.block + `}]}`))
		if err != nil {
			t.Fatal(err)
		}
		cfg.lic = licensed(t)
		lanes, err := buildLanes(cfg, nil, &analyzerDeps{cfg: cfg.Analyzer, provider: p})
		if err != nil {
			t.Fatal(err)
		}
		sql := inspect.Statement{Protocol: inspect.Postgres, Direction: inspect.FromClient, Text: "DELETE FROM t", Operation: inspect.OpDelete}
		if denied := lanes[0].policy.Evaluate(sql).Denied; denied == v.Allowed {
			t.Errorf("%s: the postgres lane denied=%v, the mcp lane allowed=%v", c.block, denied, v.Allowed)
		}
	}
}

// On a SQL tool the model reads the SQL, once, and not the call around it.
func TestMCPLaneAnalyzerReadsTheSQLOfASQLTool(t *testing.T) {
	p := &toolRiskProvider{}
	ls, sink := openMCPAnalyzerSession(t, `{"trigger":{"operations":["call"]},"high":"block"}`,
		`{"sql":{"execute_sql":{"argument":"query","dialect":"googlesql"}}}`, p, nil, nil)
	if v := ls.ToolCall(context.Background(), "execute_sql", []byte(`{"query":"UPDATE ds.t SET a = 1 WHERE true"}`)); !v.Allowed {
		t.Fatalf("verdict %+v", v)
	}
	sent := p.sent()
	if len(sent) != 1 || !strings.Contains(sent[0], "Operation: update") || !strings.Contains(sent[0], "UPDATE ds.t SET a = 1 WHERE true") || strings.Contains(sent[0], "Tool:") {
		t.Fatalf("the model was sent %q", sent)
	}
	if rows := judged(sink); len(rows) != 2 || rows[0].Metadata[analyzer.MetadataAIStatus] != analyzer.StatusSkipped ||
		rows[1].Metadata[analyzer.MetadataAIStatus] != analyzer.StatusOK || rows[1].Metadata[policy.AnnotationRiskLevel] != "low" {
		t.Fatalf("rows %+v", rows)
	}
}

// Releases from 1.289.0 decode an analyzer on an mcp lane and refuse the
// whole document, so the plane serves one only to a build that reads it.
func TestAnMCPAnalyzerLaneNeedsTheCapability(t *testing.T) {
	old := Handshake{Version: "1.295.1", Capabilities: []string{"protocol:mcp", "protocol:postgres", "address_url", "rule:ai_analysis", "rule:operation"}}
	current := Handshake{Capabilities: append(slices.Clone(old.Capabilities), CapabilityMCPAnalyzer)}
	ai := policy.Rule{Name: "risky", Type: policy.MatchAIAnalysis, HighRisk: "block"}
	mcp := ListenerConfig{Name: "tools", Protocol: "mcp", Listen: "127.0.0.1:18901", Upstream: "https://h/mcp"}
	withBlock, withRule := mcp, mcp
	withBlock.Analyzer = &LaneAnalyzerConfig{HighRisk: "block"}
	withRule.Guardrails = &GuardrailsConfig{Rules: []policy.Rule{ai}}
	optedOut := mcp
	optedOut.Guardrails = &GuardrailsConfig{Rules: []policy.Rule{}}
	inherited := &GuardrailsConfig{Rules: []policy.Rule{ai}}
	pgBlock := ListenerConfig{Name: "appdb", Protocol: "postgres", Listen: "127.0.0.1:15432", Upstream: "db:5432", Analyzer: &LaneAnalyzerConfig{HighRisk: "block"}}
	for _, c := range []struct {
		name  string
		cfg   Config
		needs bool
	}{
		{"analyzer block", Config{Listeners: []ListenerConfig{withBlock}}, true},
		{"ai_analysis rule", Config{Listeners: []ListenerConfig{withRule}}, true},
		{"inherited ai_analysis rule", Config{Guardrails: inherited, Listeners: []ListenerConfig{mcp}}, true},
		{"inherited rule, opted out", Config{Guardrails: inherited, Listeners: []ListenerConfig{optedOut}}, false},
		{"no analyzer", Config{Listeners: []ListenerConfig{mcp}}, false},
		{"analyzer on a postgres lane", Config{Listeners: []ListenerConfig{pgBlock, mcp}}, false},
	} {
		err := CheckServable(c.cfg, old)
		if refused := err != nil && strings.Contains(err.Error(), CapabilityMCPAnalyzer); refused != c.needs || (!c.needs && err != nil) {
			t.Errorf("%s: an older build: %v", c.name, err)
		}
		if err := CheckServable(c.cfg, current); err != nil {
			t.Errorf("%s: a build that reads it was refused: %v", c.name, err)
		}
	}
	if !slices.Contains(SidecarCapabilities(), CapabilityMCPAnalyzer) {
		t.Fatalf("the header lacks %s: %v", CapabilityMCPAnalyzer, SidecarCapabilities())
	}
}
