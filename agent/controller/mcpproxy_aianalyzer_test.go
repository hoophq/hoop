package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	aianalyzer "github.com/hoophq/hoop/common/aianalyzer"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/common/proto/spectypes"
	"github.com/hoophq/mcpproxy/audit"
	"github.com/hoophq/mcpproxy/checks"
	mcpconfig "github.com/hoophq/mcpproxy/config"
	"github.com/hoophq/mcpproxy/inspect"
	"github.com/hoophq/mcpproxy/jsonrpc"
	"github.com/hoophq/mcpproxy/mcp"
	"github.com/vmihailenco/msgpack/v5"
)

// fakeEngine stands in for the LLM-backed analyzer. It records what it was
// asked to classify so a test can assert the model sees the tool call rather
// than the HTTP envelope carrying it.
type fakeEngine struct {
	decision *aianalyzer.Decision
	err      error

	calls   int
	gotVerb string
	gotTool string
	gotBody []byte
}

func (f *fakeEngine) AnalyzeRequest(_ context.Context, method, target string, body []byte) (*aianalyzer.Decision, error) {
	f.calls++
	f.gotVerb, f.gotTool, f.gotBody = method, target, body
	if f.err != nil {
		return nil, f.err
	}
	return f.decision, nil
}

// recordingSink captures the audit events a check emits.
type recordingSink struct{ events []audit.Event }

func (s *recordingSink) Emit(_ context.Context, ev audit.Event) { s.events = append(s.events, ev) }

func (s *recordingSink) ofType(t audit.EventType) []audit.Event {
	var out []audit.Event
	for _, ev := range s.events {
		if ev.Type == t {
			out = append(out, ev)
		}
	}
	return out
}

// stubSession is the minimal inspect.Session a check needs. The real one lives
// in mcpproxy's session package and needs a live gateway.
type stubSession struct {
	id       string
	counters map[string]*int64
	meta     map[string]any
}

func newStubSession(id string) *stubSession {
	return &stubSession{id: id, counters: map[string]*int64{}, meta: map[string]any{}}
}

func (s *stubSession) ID() string                 { return s.id }
func (s *stubSession) Identity() inspect.Identity { return inspect.Identity{Subject: "user-1"} }
func (s *stubSession) ToolKnown(_, _ string) bool { return true }
func (s *stubSession) Counter(name string) *int64 {
	if s.counters[name] == nil {
		s.counters[name] = new(int64(0))
	}
	return s.counters[name]
}
func (s *stubSession) Meta(key string, mk func() any) any {
	if _, ok := s.meta[key]; !ok {
		s.meta[key] = mk()
	}
	return s.meta[key]
}

// toolCallMsg builds the inspect.Msg the pipeline sees for one tools/call.
func toolCallMsg(t *testing.T, tool, args string) *inspect.Msg {
	t.Helper()
	params, err := json.Marshal(mcp.ToolsCallParams{Name: tool, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	env, err := jsonrpc.Parse([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`, mcp.MethodToolsCall, params)))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	return &inspect.Msg{Dir: inspect.C2S, Backend: "laptop-mcp", Env: env}
}

// newTestAnalyzer wires a check around a fake engine, capturing shipped
// verdicts.
func newTestAnalyzer(engine aianalyzer.Analyzer, sink audit.Sink) (*mcpAnalyzer, *[][]byte) {
	var shipped [][]byte
	a := &mcpAnalyzer{
		engine:      engine,
		sink:        sink,
		sid:         "sid-analyzer",
		emitVerdict: func(b []byte) { shipped = append(shipped, b) },
	}
	return a, &shipped
}

func decodeVerdict(t *testing.T, encoded []byte) spectypes.AIAnalyzerVerdict {
	t.Helper()
	v, err := spectypes.DecodeAIAnalyzerVerdict(encoded)
	if err != nil {
		t.Fatalf("verdict does not decode with the key the gateway audit plugin reads: %v", err)
	}
	return *v
}

// A high-risk tool call configured to block must be refused before it reaches
// the backend, with a JSON-RPC error the MCP client can render.
func TestMCPAnalyzerBlocksHighRiskToolCall(t *testing.T) {
	engine := &fakeEngine{decision: &aianalyzer.Decision{
		Outcome:     aianalyzer.OutcomeBlock,
		RiskLevel:   aianalyzer.RiskLevelHigh,
		Title:       "Destructive delete",
		Explanation: "Removes every record without a filter.",
		RuleName:    "mcp-rule",
	}}
	sink := &recordingSink{}
	a, shipped := newTestAnalyzer(engine, sink)

	dec := a.Inspect(context.Background(), newStubSession("sid-analyzer"),
		toolCallMsg(t, "delete_everything", `{"confirm":true}`))

	if dec.Verdict != inspect.Deny {
		t.Fatalf("verdict = %v, want Deny: a high-risk call reached the backend", dec.Verdict)
	}
	if dec.Rule != nameMCPAnalyzer {
		t.Errorf("rule = %q, want %q", dec.Rule, nameMCPAnalyzer)
	}
	if dec.Reply == nil || dec.Reply.Error == nil {
		t.Fatal("deny carried no JSON-RPC error; the client would hang waiting for a reply")
	}
	if dec.Reply.Error.Code != jsonrpc.CodePolicyDenied {
		t.Errorf("error code = %d, want %d", dec.Reply.Error.Code, jsonrpc.CodePolicyDenied)
	}

	// The check must NOT audit the denial itself: mcpproxy's gateway audits
	// every Deny centrally, from the rule and reason on this decision. See
	// TestMCPAnalyzerEmitsExactlyOneDenialEvent for the whole-pipeline proof.
	if denied := sink.ofType(audit.EventToolDenied); len(denied) != 0 {
		t.Fatalf("the check emitted %d tool_denied events of its own; the gateway audits the deny, so each one is a duplicate", len(denied))
	}
	// The reason is both the audit record's reason and the JSON-RPC error the
	// MCP client renders, so it has to name the tool and carry the model's
	// explanation — the detail the removed local event used to hold.
	for _, want := range []string{"delete_everything", "high", "Destructive delete", "Removes every record without a filter."} {
		if !strings.Contains(dec.Reason, want) {
			t.Errorf("deny reason %q omits %q; the reviewer and the caller both read this string", dec.Reason, want)
		}
	}
	if dec.Reply.Error.Message != dec.Reason {
		t.Errorf("client sees %q but the audit records %q", dec.Reply.Error.Message, dec.Reason)
	}

	if len(*shipped) != 1 {
		t.Fatalf("shipped %d verdicts, want 1", len(*shipped))
	}
	v := decodeVerdict(t, (*shipped)[0])
	if v.Outcome != string(aianalyzer.OutcomeBlock) || v.RiskLevel != string(aianalyzer.RiskLevelHigh) {
		t.Errorf("verdict = %+v, want a high-risk block", v)
	}
}

// Warn forwards the call. The distinction from block is the whole point of the
// risk tiers: a medium-risk call is recorded, not refused.
func TestMCPAnalyzerWarnForwardsAndStillRecords(t *testing.T) {
	engine := &fakeEngine{decision: &aianalyzer.Decision{
		Outcome:   aianalyzer.OutcomeWarn,
		RiskLevel: aianalyzer.RiskLevelMedium,
		Title:     "Bulk read",
		RuleName:  "mcp-rule",
	}}
	sink := &recordingSink{}
	a, shipped := newTestAnalyzer(engine, sink)

	dec := a.Inspect(context.Background(), newStubSession("sid-analyzer"),
		toolCallMsg(t, "export_users", `{"limit":100000}`))

	if dec.Verdict != inspect.Allow {
		t.Fatalf("verdict = %v, want Allow: warn must forward the call", dec.Verdict)
	}
	if denied := sink.ofType(audit.EventToolDenied); len(denied) != 0 {
		t.Errorf("a warned call emitted %d denial events", len(denied))
	}
	if len(*shipped) != 1 {
		t.Fatalf("shipped %d verdicts, want 1: a forwarded call still has to be counted", len(*shipped))
	}
	if v := decodeVerdict(t, (*shipped)[0]); v.Outcome != string(aianalyzer.OutcomeWarn) {
		t.Errorf("verdict outcome = %q, want warn", v.Outcome)
	}
}

// A broken provider must not deny tool calls. Failing closed here would let an
// unreachable LLM endpoint take down every MCP connection using the feature.
func TestMCPAnalyzerFailsOpenWhenTheProviderBreaks(t *testing.T) {
	engine := &fakeEngine{err: errors.New("connection refused")}
	sink := &recordingSink{}
	a, shipped := newTestAnalyzer(engine, sink)

	dec := a.Inspect(context.Background(), newStubSession("sid-analyzer"),
		toolCallMsg(t, "whoami", `{}`))

	if dec.Verdict != inspect.Allow {
		t.Fatalf("verdict = %v, want Allow: a broken analyzer must not block tool calls", dec.Verdict)
	}
	// Silent permissiveness is the trap: the failure has to be auditable.
	errs := sink.ofType(audit.EventError)
	if len(errs) != 1 {
		t.Fatalf("emitted %d error events, want 1: a chronically broken provider must be visible", len(errs))
	}
	if !strings.Contains(errs[0].Reason, "connection refused") {
		t.Errorf("error event reason = %q, want the provider failure", errs[0].Reason)
	}
	if len(*shipped) != 0 {
		t.Errorf("shipped %d verdicts for a call that was never classified", len(*shipped))
	}
}

// hangingEngine is a provider that accepts the call and never answers on its
// own: it returns only when the context it was handed expires. It records that
// context's deadline, which is the thing under test.
type hangingEngine struct {
	mu       sync.Mutex
	deadline time.Time
	hadOne   bool
}

func (e *hangingEngine) AnalyzeRequest(ctx context.Context, _, _ string, _ []byte) (*aianalyzer.Decision, error) {
	dl, ok := ctx.Deadline()
	e.mu.Lock()
	e.deadline, e.hadOne = dl, ok
	e.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (e *hangingEngine) observed() (time.Time, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deadline, e.hadOne
}

// A provider that accepts the connection and then stops answering must cost
// the tool call mcpAnalyzerTimeout, not the caller's budget.
//
// The context reaching Inspect on the tool-call path is the gateway's request
// context, and its deadline is the held-call budget — thirty minutes, because
// a call parked for human review is meant to wait that long. Inheriting it
// means a hung provider stalls the call for the whole budget: no verdict, no
// error, no result, indistinguishable from a slow tool. The fail-open path
// exists for exactly this failure and never runs.
//
// The assertion is on the deadline the engine was handed rather than on
// wall-clock elapsed time: the bound is 30 seconds, and a test that waits it
// out to prove a point is 30 seconds every run. The deadline is the same fact,
// observed directly — under the unfixed code it is the caller's, half an hour
// out.
func TestMCPAnalyzerBoundsAHangingProvider(t *testing.T) {
	engine := &hangingEngine{}
	sink := &recordingSink{}
	a, shipped := newTestAnalyzer(engine, sink)

	// The caller's context, as the gateway builds it for a held tool call.
	ctx, cancel := context.WithTimeout(context.Background(), pb.MCPHeldCallBudget)
	defer cancel()
	callerDeadline, _ := ctx.Deadline()

	done := make(chan inspect.Decision, 1)
	go func() {
		done <- a.Inspect(ctx, newStubSession("sid-analyzer"), toolCallMsg(t, "slow_tool", `{}`))
	}()

	// Give the engine a moment to record the context, then release it the way
	// its own deadline would.
	var (
		deadline time.Time
		ok       bool
	)
	for range 100 {
		if deadline, ok = engine.observed(); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok {
		t.Fatal("the analyzer handed the provider a context with no deadline: a hung provider would stall the tool call forever")
	}
	if !deadline.Before(callerDeadline) {
		t.Fatalf("the provider inherited the caller's deadline (%v); a hung provider stalls the tool call for the whole held-call budget",
			time.Until(deadline).Round(time.Second))
	}
	if bound := time.Until(deadline); bound > mcpAnalyzerTimeout {
		t.Fatalf("provider deadline is %v out, want at most mcpAnalyzerTimeout (%v)", bound.Round(time.Second), mcpAnalyzerTimeout)
	}

	// Cancelling the caller stands in for the analyzer's own deadline firing:
	// either way the engine returns a context error, and what matters is what
	// Inspect does with it.
	cancel()
	select {
	case dec := <-done:
		if dec.Verdict != inspect.Allow {
			t.Fatalf("verdict = %v, want Allow: a provider that never answered must not deny the call", dec.Verdict)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Inspect never returned after the provider's context expired")
	}

	// The stall has to be auditable, exactly as an outright refusal is.
	if errs := sink.ofType(audit.EventError); len(errs) != 1 {
		t.Fatalf("emitted %d error events, want 1: a chronically hung provider must be visible", len(errs))
	}
	if len(*shipped) != 0 {
		t.Errorf("shipped %d verdicts for a call that was never classified", len(*shipped))
	}
}

// Exactly one mcp.tool_denied event per blocked call.
//
// mcpproxy audits every Deny centrally, at the point the pipeline's verdict is
// applied (gateway/http.go, auditDeny), using the rule and reason the check
// returned. A check that also emits its own denial event therefore writes the
// record twice: the reviewer sees the same refusal twice in the session
// timeline and the session metrics count one blocked call as two.
//
// The pipeline is assembled and run the way the gateway runs it, since the
// duplicate only exists in the interaction between the check and its caller —
// a unit test on the check alone cannot see it.
func TestMCPAnalyzerEmitsExactlyOneDenialEvent(t *testing.T) {
	sessionID := "sid-one-denial"
	backend, _ := newTunnelPair(t, sessionID, nil)
	agent := backend.agent
	t.Cleanup(func() { agent.closeMCPProxyConnections(sessionID) })

	fake := newFakeOpenAI(t, "HighRiskAISessionAnalyzer")
	connParams := agent.connectionParams(sessionID)
	connParams.AISessionAnalyzer = analyzerParams(fake.srv.URL, "block_execution")
	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("env: %v", err)
	}

	gw, err := agent.mcpGatewayFor(sessionID, connenv, connParams,
		mcpProxyOpts(connenv, connParams, sessionID, "1"),
		map[string][]byte{pb.SpecGatewaySessionID: []byte(sessionID)})
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}

	h := gw.Handler()
	_, sid, _ := mcpPost(t, h, "", mcpInitialize)
	if sid == "" {
		t.Fatal("initialize did not open a session")
	}
	mcpPost(t, h, sid, mcpToolsList)
	_, _, body := mcpPost(t, h, sid,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`)
	if !strings.Contains(body, "error") {
		t.Fatalf("the high-risk call was not blocked: %s", body)
	}

	// Cleanup flushes the sink, so every event the session produced has been
	// handed to the transport by the time it returns.
	agent.closeMCPProxyConnections(sessionID)

	var denials []audit.Event
	for _, pkt := range backend.agent.client.(*loopTransport).packetsOfType(pbclient.MCPProxyConnectionWrite) {
		if len(pkt.Spec[pb.SpecMCPEventKey]) == 0 {
			continue
		}
		var ev audit.Event
		if err := json.Unmarshal(pkt.Payload, &ev); err != nil {
			continue // the verdict packet's payload is a bare newline
		}
		if ev.Type == audit.EventToolDenied {
			denials = append(denials, ev)
		}
	}
	if len(denials) != 1 {
		t.Fatalf("one blocked call produced %d mcp.tool_denied events, want 1", len(denials))
	}
	if denials[0].Rule != nameMCPAnalyzer {
		t.Errorf("denial rule = %q, want %q", denials[0].Rule, nameMCPAnalyzer)
	}
	// The surviving event is the gateway's, so the reason is the only place
	// the human-readable detail can live. It must still say what was refused
	// and why.
	for _, want := range []string{"whoami", "high"} {
		if !strings.Contains(denials[0].Reason, want) {
			t.Errorf("denial reason %q omits %q", denials[0].Reason, want)
		}
	}
}

// The model must see the tool call, not the transport. Every MCP request is a
// POST to the same path, so classifying the HTTP envelope would hand the model
// the identical text for a read and a destructive delete.
func TestMCPAnalyzerClassifiesTheToolCallNotTheTransport(t *testing.T) {
	engine := &fakeEngine{decision: &aianalyzer.Decision{Outcome: aianalyzer.OutcomeAllow}}
	a, _ := newTestAnalyzer(engine, &recordingSink{})

	a.Inspect(context.Background(), newStubSession("sid-analyzer"),
		toolCallMsg(t, "drop_table", `{"table":"users"}`))

	if engine.gotTool != "drop_table" {
		t.Errorf("classified target %q, want the tool name", engine.gotTool)
	}
	if engine.gotVerb != mcp.MethodToolsCall {
		t.Errorf("classified method %q, want %q", engine.gotVerb, mcp.MethodToolsCall)
	}
	if !strings.Contains(string(engine.gotBody), `"users"`) {
		t.Errorf("classified body %q, want the tool arguments", engine.gotBody)
	}
}

// Only client-to-server tool calls are worth an LLM round trip. Analyzing
// tools/list, notifications or server-initiated messages would add seconds of
// latency and provider cost to traffic no risk tier applies to.
func TestMCPAnalyzerSkipsEverythingButToolCalls(t *testing.T) {
	listEnv, err := jsonrpc.Parse([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// prompts/get also carries a "name" param, so a check that only guards on
	// a non-empty name — and forgets the method — would classify it as a tool
	// call. tools/list cannot catch that: its params are empty.
	promptEnv, err := jsonrpc.Parse([]byte(`{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"summarize"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s2c := toolCallMsg(t, "whoami", `{}`)
	s2c.Dir = inspect.S2C

	for _, tc := range []struct {
		name string
		msg  *inspect.Msg
	}{
		{"tools/list", &inspect.Msg{Dir: inspect.C2S, Env: listEnv}},
		{"prompts/get", &inspect.Msg{Dir: inspect.C2S, Env: promptEnv}},
		{"server-initiated message", s2c},
		{"nil envelope", &inspect.Msg{Dir: inspect.C2S}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &fakeEngine{decision: &aianalyzer.Decision{Outcome: aianalyzer.OutcomeBlock}}
			a, _ := newTestAnalyzer(engine, &recordingSink{})

			if dec := a.Inspect(context.Background(), newStubSession("sid-analyzer"), tc.msg); dec.Verdict != inspect.Allow {
				t.Fatalf("verdict = %v, want Allow", dec.Verdict)
			}
			if engine.calls != 0 {
				t.Fatalf("spent %d model calls on a %s", engine.calls, tc.name)
			}
		})
	}
}

// Each analyzed call needs its own sequence number: the gateway dedupes on
// (ConnID, Seq) and would fold repeated verdicts into a single counted request.
func TestMCPAnalyzerStampsAMonotonicSequence(t *testing.T) {
	engine := &fakeEngine{decision: &aianalyzer.Decision{Outcome: aianalyzer.OutcomeAllow}}
	a, shipped := newTestAnalyzer(engine, &recordingSink{})

	for range 3 {
		a.Inspect(context.Background(), newStubSession("sid-analyzer"), toolCallMsg(t, "whoami", `{}`))
	}
	if len(*shipped) != 3 {
		t.Fatalf("shipped %d verdicts, want 3", len(*shipped))
	}
	for i, enc := range *shipped {
		v := decodeVerdict(t, enc)
		if v.Seq != uint64(i+1) {
			t.Fatalf("verdict %d carries seq %d, want %d; the gateway would dedupe it away", i, v.Seq, i+1)
		}
		if v.ConnID != "sid-analyzer" {
			t.Errorf("verdict %d carries conn id %q, want the session id", i, v.ConnID)
		}
	}
}

// Oversized arguments must be capped before they reach the provider, and the
// truncation marked so the model does not read a severed JSON object as whole.
func TestMCPAnalyzerCapsOversizedArguments(t *testing.T) {
	engine := &fakeEngine{decision: &aianalyzer.Decision{Outcome: aianalyzer.OutcomeAllow}}
	a, _ := newTestAnalyzer(engine, &recordingSink{})

	big := fmt.Sprintf(`{"blob":%q}`, strings.Repeat("x", mcpMaxAnalyzedArgsBytes*2))
	a.Inspect(context.Background(), newStubSession("sid-analyzer"), toolCallMsg(t, "upload", big))

	if len(engine.gotBody) > mcpMaxAnalyzedArgsBytes+len("\n...[truncated]") {
		t.Fatalf("sent %d bytes to the provider, want at most %d",
			len(engine.gotBody), mcpMaxAnalyzedArgsBytes+len("\n...[truncated]"))
	}
	if !strings.HasSuffix(string(engine.gotBody), "[truncated]") {
		t.Error("truncated arguments are not marked; the model reads a severed object as the whole call")
	}
}

// The analyzer must run after the deterministic checks and before the terminal
// audit tap.
//
// Both ends matter. Ahead of the cheap checks it would spend a model call on a
// tool the deny-list already refuses; after the audit tap it would be skipped
// entirely for denied calls, because mcpproxy's runPipeline stops at the first
// non-Allow verdict.
func TestMCPAnalyzerRunsBeforeTheAuditTap(t *testing.T) {
	base := checks.Assemble(mcpconfig.Policy{}, inspect.Hooks{}, audit.Discard, false)
	a, _ := newTestAnalyzer(&fakeEngine{}, nil)

	got := insertBeforeAuditTap(base, a)
	if len(got) != len(base)+1 {
		t.Fatalf("pipeline has %d checks, want %d", len(got), len(base)+1)
	}
	if got[len(got)-1].Name() != checks.NameAuditTap {
		t.Fatalf("last check is %q, want the audit tap to stay terminal", got[len(got)-1].Name())
	}
	if got[len(got)-2].Name() != nameMCPAnalyzer {
		t.Fatalf("analyzer sits at %q, want it immediately before the audit tap", got[len(got)-2].Name())
	}
	// Every original check survives, in order.
	for i, chk := range base[:len(base)-1] {
		if got[i].Name() != chk.Name() {
			t.Fatalf("check %d = %q, want %q: insertion reordered the pipeline", i, got[i].Name(), chk.Name())
		}
	}
}

// A pipeline with no terminal tap still gets the analyzer appended rather than
// dropped.
func TestInsertBeforeAuditTapWithoutATap(t *testing.T) {
	a, _ := newTestAnalyzer(&fakeEngine{}, nil)
	base := []inspect.Check{checks.NewPolicy(mcpconfig.Policy{})}

	got := insertBeforeAuditTap(base, a)
	if len(got) != 2 || got[1].Name() != nameMCPAnalyzer {
		t.Fatalf("pipeline = %v, want the analyzer appended", checkNames(got))
	}
}

func checkNames(cs []inspect.Check) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name())
	}
	return out
}

// The verdict must reach the gateway on a packet the audit plugin already
// understands: tagged as an MCP protocol event, carrying the verdict under the
// spec key the plugin reads. Get either wrong and the metrics stay empty while
// everything appears to work.
func TestMCPVerdictEmitterShipsAGatewayReadablePacket(t *testing.T) {
	transport := newBlockingTransport()
	close(transport.release) // never block
	agent := New(transport, nil, nil)

	decision := &aianalyzer.Decision{
		Outcome:   aianalyzer.OutcomeBlock,
		RiskLevel: aianalyzer.RiskLevelHigh,
		Title:     "Destructive delete",
		RuleName:  "mcp-rule",
	}
	encoded, err := decision.Verdict(7, "sid-emit").Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	sink := agent.mcpAuditSink("sid-emit", map[string][]byte{
		pb.SpecGatewaySessionID: []byte("sid-emit"),
	})
	mcpVerdictEmitter(sink)(encoded)
	sink.stop()
	waitSinkStopped(t, sink)

	sent := transport.packets()
	if len(sent) != 1 {
		t.Fatalf("sent %d packets, want 1", len(sent))
	}
	pkt := sent[0]
	if pkt.Type != pbclient.MCPProxyConnectionWrite {
		t.Fatalf("packet type = %q, want %q", pkt.Type, pbclient.MCPProxyConnectionWrite)
	}
	if len(pkt.Spec[pb.SpecMCPEventKey]) == 0 {
		t.Fatal("verdict packet is not tagged as an MCP event; the gateway would forward it to the MCP client as response bytes")
	}
	if string(pkt.Spec[pb.SpecGatewaySessionID]) != "sid-emit" {
		t.Errorf("session id = %q, want sid-emit", pkt.Spec[pb.SpecGatewaySessionID])
	}
	raw := pkt.Spec[spectypes.AIAnalyzerInfoKey]
	if len(raw) == 0 {
		t.Fatalf("verdict absent from spec key %q; session metrics would stay empty", spectypes.AIAnalyzerInfoKey)
	}
	v := decodeVerdict(t, raw)
	if v.Seq != 7 || v.RiskLevel != string(aianalyzer.RiskLevelHigh) {
		t.Errorf("decoded verdict = %+v, want the shipped one", v)
	}
	// The wire layout is mirrored by value in two packages; a field rename on
	// either side has to fail here rather than silently decode to zero.
	var loose map[string]any
	if err := msgpack.Unmarshal(raw, &loose); err != nil {
		t.Fatalf("verdict is not msgpack: %v", err)
	}
	for _, key := range []string{"outcome", "risk_level", "seq", "conn_id"} {
		if _, ok := loose[key]; !ok {
			t.Errorf("verdict is missing msgpack field %q the gateway reads", key)
		}
	}
}
