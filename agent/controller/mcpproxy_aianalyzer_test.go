package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

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

	if denied := sink.ofType(audit.EventToolDenied); len(denied) != 1 {
		t.Fatalf("emitted %d tool_denied events, want 1", len(denied))
	} else if denied[0].Tool != "delete_everything" {
		t.Errorf("audit names tool %q, want delete_everything", denied[0].Tool)
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
	agent.mcpVerdictEmitter("sid-emit")(encoded)

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
