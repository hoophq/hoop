package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// toolRiskProvider rates a call to delete_customer or a DROP TABLE high and
// anything else low, and keeps what it was sent. err set fails every call.
// With entered set, each call reports there and answers once release closes.
type toolRiskProvider struct {
	mu      sync.Mutex
	prompts []string
	err     error
	entered chan struct{}
	release chan struct{}
}

func (p *toolRiskProvider) Name() string { return "stub" }

func (p *toolRiskProvider) Classify(_ context.Context, _, content string) (*analyzer.Result, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, content)
	p.mu.Unlock()
	if p.entered != nil {
		p.entered <- struct{}{}
		<-p.release
	}
	if p.err != nil {
		return nil, p.err
	}
	switch {
	case strings.Contains(content, "Tool: delete_customer"):
		return &analyzer.Result{RiskLevel: analyzer.RiskHigh, Title: "deletes a customer"}, nil
	case strings.Contains(content, "DROP TABLE"):
		return &analyzer.Result{RiskLevel: analyzer.RiskHigh, Title: "drops a table"}, nil
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

const heldCallBlock = `{"high":"require_approval","approval_rule":"tool-approvers"}`

// A hold files the call with the plane under the lane and its approval rule.
// An approval the plane spends lets the call through, and the row names the
// review.
func TestMCPLaneHoldReleasesAnApprovedCall(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusCreated, `{"forward":true,"review":{"id":"9f97","status":"EXECUTED"}}`)
	ls, sink := openMCPAnalyzerSession(t, heldCallBlock, "", &toolRiskProvider{}, cp, nil)
	if v := ls.ToolCall(context.Background(), "delete_customer", []byte(`{"id":"7"}`)); !v.Allowed || v.Held {
		t.Fatalf("approved call verdict: %+v", v)
	}
	if len(*calls) != 1 {
		t.Fatalf("the plane saw %d review requests, want 1", len(*calls))
	}
	c := (*calls)[0]
	if c.path != "/api/sidecars/reviews" || c.listen != "tools" || c.rule != "tool-approvers" ||
		c.payload != base64.StdEncoding.EncodeToString([]byte("delete_customer\n{\"id\":\"7\"}")) {
		t.Fatalf("review request: %+v", c)
	}
	if r := judged(sink)[0]; !r.Allowed || r.Metadata[analyzer.MetadataReviewID] != "9f97" || r.Metadata[analyzer.MetadataReviewMode] != "hold" {
		t.Fatalf("released row: %+v", r)
	}
}

// A rejected or expired review refuses the one call with the plane's reason,
// and the session goes on.
func TestMCPLaneHoldRefusesARejectedCall(t *testing.T) {
	for status, want := range map[string]string{
		"REJECTED": `the approval request was rejected: "not on a friday" (approval 9f97)`,
		"EXPIRED":  "the approval request expired",
	} {
		cp, _ := reviewPlane(t, http.StatusCreated,
			`{"forward":false,"review":{"id":"9f97","status":"`+status+`","rejection_reason":"not on a friday"}}`)
		ls, _ := openMCPAnalyzerSession(t, heldCallBlock, "", &toolRiskProvider{}, cp, nil)
		ctx := context.Background()
		if v := ls.ToolCall(ctx, "delete_customer", []byte(`{"id":"7"}`)); v.Allowed || v.Held || v.Rule != "tools" || !strings.Contains(v.Message, want) {
			t.Fatalf("%s: verdict %+v", status, v)
		}
		if v := ls.ToolCall(ctx, "echo", []byte(`{}`)); !v.Allowed {
			t.Fatalf("%s: the call after a refusal was refused: %+v", status, v)
		}
	}
}

// A pending review is a held refusal: at once in return mode, at the lane's
// hold limit otherwise. Both name the approval.
func TestMCPLaneHoldAnswersAPendingCallWithTheApproval(t *testing.T) {
	for _, c := range []struct {
		name, block, want string
		claims            int
	}{
		{"return", `{"high":"require_approval","approval_rule":"tool-approvers","review_mode":"return"}`, "approval 9f97", 0},
		{"hold", heldCallBlock, "still waiting for approval after 30ms", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			cp, calls := reviewPlane(t, http.StatusCreated, `{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)
			ls, _ := openMCPAnalyzerSession(t, c.block, "", &toolRiskProvider{}, cp, nil)
			ls.(*mcpLaneSession).holdWait = 30 * time.Millisecond
			v := ls.ToolCall(context.Background(), "delete_customer", []byte(`{"id":"7"}`))
			if v.Allowed || !v.Held || !strings.Contains(v.Message, c.want) || !strings.Contains(v.Message, "9f97") {
				t.Fatalf("verdict %+v", v)
			}
			if claims := len(*calls) - 1; claims != c.claims {
				t.Fatalf("%d claims, want %d: %+v", claims, c.claims, *calls)
			}
		})
	}
}

// The hold ends before the request does, and leaves the approved call time
// to run.
func TestMCPLaneHoldEndsBeforeTheRequest(t *testing.T) {
	if mcpHoldWait <= 0 || MCPRequestTimeout-mcpHoldWait < time.Minute || mcpHoldWait > analyzer.ReviewWait {
		t.Fatalf("hold %s, request %s", mcpHoldWait, MCPRequestTimeout)
	}
}

// Close ends a hold at once, so a DELETE, an idle reap, a reload or a
// shutdown does not wait for the approver, and a later approval is not spent
// on a gone session.
func TestMCPLaneHoldEndsWithTheSession(t *testing.T) {
	filed := make(chan struct{}, 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		filed <- struct{}{}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`))
	}))
	t.Cleanup(srv.Close)
	cp := &controlPlane{url: srv.URL, cred: tokenCredential("hsc_token")}
	ls, _ := openMCPAnalyzerSession(t, heldCallBlock, "", &toolRiskProvider{}, cp, nil)
	ls.(*mcpLaneSession).holdWait = 10 * time.Second
	verdict := make(chan MCPVerdict, 1)
	go func() { verdict <- ls.ToolCall(context.Background(), "delete_customer", []byte(`{"id":"7"}`)) }()
	<-filed
	start := time.Now()
	ls.Close(context.Background())
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Close waited %s for the hold", d)
	}
	if v := <-verdict; v.Allowed || !strings.Contains(v.Message, "ended while waiting for approval") {
		t.Fatalf("verdict %+v", v)
	}
}

// The idle note compares against the mcp hold, not the 30-minute review wait.
func TestAnIdleTimeoutShorterThanTheMCPHoldIsNoted(t *testing.T) {
	cp, _ := reviewPlane(t, http.StatusCreated, `{}`)
	for _, c := range []struct {
		idle int
		want bool
	}{{60, true}, {600, false}} {
		cfg, err := LoadConfigBytes([]byte(`{"analyzer":{"provider":"stub","model":"m"},"listeners":[{"name":"tools","protocol":"mcp","listen":"127.0.0.1:18901","upstream":"https://mcp.example.com/mcp","idle_timeout_sec":` +
			strconv.Itoa(c.idle) + `,"analyzer":` + heldCallBlock + `}]}`))
		if err != nil {
			t.Fatal(err)
		}
		cfg.lic = licensed(t)
		lanes, err := buildLanes(cfg, nil, &analyzerDeps{cfg: cfg.Analyzer, provider: &toolRiskProvider{}, cp: cp})
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, n := range lanes[0].notes {
			found = found || strings.Contains(n, "before the "+mcpHoldWait.String())
		}
		if found != c.want {
			t.Errorf("idle_timeout_sec %d: noted %v, want %v: %v", c.idle, found, c.want, lanes[0].notes)
		}
	}
}

// A hold needs somewhere to file.
func TestMCPLaneHoldNeedsAPlane(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"analyzer":{"provider":"stub","model":"m"},"listeners":[{"name":"tools","protocol":"mcp","listen":"127.0.0.1:18901","upstream":"https://mcp.example.com/mcp","analyzer":{"high":"require_approval"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg.lic = licensed(t)
	if _, err := buildLanes(cfg, nil, &analyzerDeps{cfg: cfg.Analyzer, provider: &toolRiskProvider{}}); err == nil || !strings.Contains(err.Error(), "no control plane") {
		t.Fatalf("a hold with no plane and no terminal reviewer built: %v", err)
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

// On a SQL tool the model reads the call, then each SQL statement as SQL.
// An argument besides the SQL reaches the model with the call.
func TestMCPLaneAnalyzerReadsTheCallAndTheSQLOfASQLTool(t *testing.T) {
	p := &toolRiskProvider{}
	ls, sink := openMCPAnalyzerSession(t, `{"trigger":{"operations":["call"]},"high":"block"}`,
		`{"sql":{"execute_sql":{"argument":"query","dialect":"googlesql"}}}`, p, nil, nil)
	ctx := context.Background()
	if v := ls.ToolCall(ctx, "execute_sql", []byte(`{"query":"UPDATE ds.t SET a = 1 WHERE true"}`)); !v.Allowed {
		t.Fatalf("verdict %+v", v)
	}
	sent := p.sent()
	if len(sent) != 2 || sent[0] != "Protocol: mcp\nTool: execute_sql\n\nArguments:\n{\"query\":\"UPDATE ds.t SET a = 1 WHERE true\"}" ||
		!strings.Contains(sent[1], "Operation: update") || !strings.Contains(sent[1], "UPDATE ds.t SET a = 1 WHERE true") || strings.Contains(sent[1], "Tool:") {
		t.Fatalf("the model was sent %q", sent)
	}
	for i, r := range judged(sink) {
		if r.Metadata[analyzer.MetadataAIStatus] != analyzer.StatusOK || r.Metadata[policy.AnnotationRiskLevel] != "low" {
			t.Fatalf("row %d: %+v", i, r)
		}
	}

	v := ls.ToolCall(ctx, "execute_sql", []byte(`{"query":"SELECT 1","note":"DROP TABLE customers"}`))
	if v.Allowed || v.Rule != "tools" || v.Message != "drops a table" {
		t.Fatalf("the call with a risky argument: %+v", v)
	}
	if sent := p.sent()[2:]; len(sent) != 1 || !strings.Contains(sent[0], `"note":"DROP TABLE customers"`) {
		t.Fatalf("the model was sent %q", sent)
	}
}

// Two calls of one session reach the model together, and Close waits for a
// call in flight before it ends the session.
func TestMCPLaneAnalyzerCallsOfOneSessionRunTogether(t *testing.T) {
	p := &toolRiskProvider{entered: make(chan struct{}, 2), release: make(chan struct{})}
	ls, sink := openMCPAnalyzerSession(t, `{"high":"block"}`, "", p, nil, nil)
	ctx := context.Background()
	var wg sync.WaitGroup
	verdicts := make([]MCPVerdict, 2)
	for i := range verdicts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			verdicts[i] = ls.ToolCall(ctx, "echo", []byte(`{"n":`+strconv.Itoa(i)+`}`))
		}()
	}
	for i := range verdicts {
		select {
		case <-p.entered:
		case <-time.After(5 * time.Second):
			close(p.release)
			t.Fatalf("call %d did not reach the model while the other waited there", i+1)
		}
	}

	closed := make(chan struct{}, 2)
	for range 2 {
		go func() {
			ls.Close(ctx)
			closed <- struct{}{}
		}()
	}
	select {
	case <-closed:
		close(p.release)
		t.Fatal("a Close returned while two calls were in flight")
	case <-time.After(100 * time.Millisecond):
	}
	close(p.release)
	wg.Wait()
	<-closed
	<-closed
	for i, v := range verdicts {
		if !v.Allowed {
			t.Errorf("call %d: %+v", i, v)
		}
	}
	if v := ls.ToolCall(ctx, "echo", []byte(`{}`)); v.Allowed || v.Message != "session closed" {
		t.Fatalf("a call after Close: %+v", v)
	}
	events := sink.Events()
	if len(events) == 0 || events[len(events)-1].Kind != audit.KindSessionEnd || len(judged(sink)) != 2 {
		t.Fatalf("the trail does not end with the session after both calls: %+v", events)
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
