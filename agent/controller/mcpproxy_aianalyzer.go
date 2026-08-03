package controller

// AI session analyzer for the protocol-aware MCP path (ADR-0004).
//
// The HTTP proxy consults the analyzer through libhoop's injected Analyzer
// contract (httpproxy_aianalyzer.go). That seam does not exist here: libhoop's
// NewMCPProxy takes an http.Handler, not an analyzer, and by the time bytes
// reach it they are an HTTP envelope around JSON-RPC rather than the tool call
// worth classifying. mcpproxy already owns the abstraction — inspect.Check, a
// pipeline stage — so the analyzer is one, appended last, just ahead of the
// terminal audit tap.
//
// What gets classified is the tool call, not the transport: the tool name and
// its arguments, rendered as text. An HTTP request line would be meaningless
// here (every call is POST /mcp).

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	aianalyzer "github.com/hoophq/hoop/common/aianalyzer"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/mcpproxy/audit"
	"github.com/hoophq/mcpproxy/checks"
	"github.com/hoophq/mcpproxy/inspect"
	"github.com/hoophq/mcpproxy/jsonrpc"
	"github.com/hoophq/mcpproxy/mcp"
)

// nameMCPAnalyzer is the audit rule name for this stage. It is not one of
// mcpproxy's checks.Name* constants because the check is hoop's, not the
// library's.
const nameMCPAnalyzer = "ai_analyzer"

// mcpMaxAnalyzedArgsBytes caps how much of a tool call's arguments reach the
// model, bounding latency and token cost. It mirrors the HTTP path's body cap
// (common/aianalyzer.maxAnalyzedBodyBytes), which is unexported.
const mcpMaxAnalyzedArgsBytes = 8 * 1024

// mcpAnalyzerTimeout bounds one classification round trip.
//
// The context reaching Inspect on the tool-call path is the gateway's request
// context, whose deadline is the held-call budget (pb.MCPHeldCallBudget) —
// minutes, because a call parked for human review is meant to wait that long.
// Inheriting it means a provider that accepts the connection and then never
// answers stalls the tool call for the whole budget: the caller sees no
// verdict, no error and no result, just a request that hangs. That is worse
// than the outage the fail-open path was written for, because it is
// indistinguishable from a slow tool.
//
// 30s is the same order as the HTTP path's own provider timeouts and long
// enough for a real model call; past it, the failure is treated as any other
// engine error and the call is forwarded unanalyzed.
const mcpAnalyzerTimeout = 30 * time.Second

// mcpAnalyzer classifies each tools/call before it reaches the backend.
//
// It is deliberately narrow: only tools/call requests travelling C2S are
// analyzed. tools/list, initialize, notifications and every server-initiated
// message are someone else's problem — the policy, rug-pull and S2C checks
// already cover them, and paying an LLM round trip per notification would make
// the session unusable.
type mcpAnalyzer struct {
	engine aianalyzer.Analyzer
	sink   audit.Sink
	sid    string
	// emitVerdict ships the encoded verdict to the gateway so the audit
	// plugin records it and the session metrics fold it in.
	emitVerdict func(encoded []byte)
	// seq is a monotonic per-session counter stamped on each verdict; the
	// gateway dedupes on (ConnID, Seq).
	seq atomic.Uint64
}

// newMCPAnalyzer builds the analyzer check from the gateway-resolved config.
//
// It returns an error only when the provider configuration is invalid, which
// the caller treats as "no analysis" rather than "no session": a misconfigured
// provider must not take an MCP connection offline, exactly as on the HTTP
// path.
func newMCPAnalyzer(cfg *pb.AISessionAnalyzerParams, sessionID string, sink audit.Sink, emitVerdict func([]byte)) (*mcpAnalyzer, error) {
	engine, err := aianalyzer.NewClient(
		aianalyzer.ProviderConfig{
			Provider: cfg.Provider,
			APIURL:   ptrIfNonEmpty(cfg.APIURL),
			APIKey:   ptrIfNonEmpty(cfg.APIKey),
			Model:    cfg.Model,
		},
		aianalyzer.RuleConfig{
			Name:             cfg.RuleName,
			CustomPrompt:     cfg.CustomPrompt,
			LowRiskAction:    aianalyzer.Action(cfg.LowRiskAction),
			MediumRiskAction: aianalyzer.Action(cfg.MediumRiskAction),
			HighRiskAction:   aianalyzer.Action(cfg.HighRiskAction),
		},
	)
	if err != nil {
		return nil, err
	}
	return &mcpAnalyzer{engine: engine, sink: sink, sid: sessionID, emitVerdict: emitVerdict}, nil
}

// Name implements inspect.Check.
func (a *mcpAnalyzer) Name() string { return nameMCPAnalyzer }

// Inspect implements inspect.Check.
//
// Fail-open on engine error, matching the HTTP path: an unreachable or slow LLM
// provider must not deny tool calls. That is a real decision, not an oversight
// — a classifier that blocks whenever it breaks is a classifier that takes the
// connection down with it. The failure is logged and audited so a chronically
// broken provider is visible rather than silently permissive.
//
// "Slow" is bounded rather than assumed: the classification runs under
// mcpAnalyzerTimeout, not the caller's context, so a provider that accepts the
// connection and stops answering costs the tool call 30 seconds and then takes
// the same fail-open path an outright refusal does.
func (a *mcpAnalyzer) Inspect(ctx context.Context, s inspect.Session, m *inspect.Msg) inspect.Decision {
	if a.engine == nil || m == nil || m.Env == nil || m.Dir != inspect.C2S {
		return inspect.AllowAll
	}
	if m.Env.Method != mcp.MethodToolsCall {
		return inspect.AllowAll
	}
	var p mcp.ToolsCallParams
	if err := json.Unmarshal(m.Env.Params, &p); err != nil || p.Name == "" {
		return inspect.AllowAll
	}
	// The exposed name may be an override. Classify the real one, agreeing
	// with the audit tap and guardrails: a call renamed by the catalog
	// overrides would otherwise be logged under a name appearing nowhere
	// else in the session.
	tool := checks.RealToolName(s, p.Name)

	// The caller's context is the gateway's request context, whose deadline is
	// the held-call budget. Classification gets its own, far shorter one — see
	// mcpAnalyzerTimeout for why inheriting the budget makes a hung provider
	// indistinguishable from a slow tool.
	actx, cancel := context.WithTimeout(ctx, mcpAnalyzerTimeout)
	defer cancel()

	decision, err := a.engine.AnalyzeRequest(actx, mcp.MethodToolsCall, tool, mcpAnalyzedArgs(p.Arguments))
	if err != nil {
		log.With("sid", a.sid, "tool", tool).Warnf("ai session analyzer failed, forwarding the call unanalyzed: %v", err)
		// ctx, not actx: the event must still be emitted when what expired is
		// the analyzer's own deadline.
		a.emit(ctx, s, audit.Event{
			Type: audit.EventError, Backend: m.Backend, Tool: tool,
			Rule: nameMCPAnalyzer, Reason: fmt.Sprintf("ai session analyzer failed: %v", err),
		})
		return inspect.AllowAll
	}
	if decision == nil {
		return inspect.AllowAll
	}

	a.shipVerdict(decision, tool)

	log.With("sid", a.sid, "tool", tool).Infof("mcp ai session analyzer verdict, outcome=%s risk=%s rule=%q title=%q",
		decision.Outcome, decision.RiskLevel, decision.RuleName, decision.Title)

	if decision.Outcome != aianalyzer.OutcomeBlock {
		// Allow and warn both forward. Warn differs only in being recorded,
		// which shipVerdict already did.
		return inspect.AllowAll
	}

	// One denial event, emitted by mcpproxy rather than here. A Deny returned
	// from a check is audited centrally by the gateway (gateway/http.go,
	// auditDeny) with this rule and this reason; emitting locally as well put
	// two mcp.tool_denied records in the session for every blocked call, which
	// double-counts blocks in the session metrics and shows the reviewer the
	// same refusal twice.
	//
	// The structured detail the local event carried is not lost. inspect.
	// Decision has no Fields to extend, so risk_level, explanation and
	// rule_name travel where they already did: the verdict shipVerdict just
	// put on the wire carries all three, typed, under the spec key the
	// gateway's audit plugin reads. What the reason has to carry is what a
	// human reads — and it is read twice over, since Denyf makes it the
	// JSON-RPC error message the MCP client renders, so the caller learns why
	// the call was refused rather than seeing a bare "policy denied".
	reason := fmt.Sprintf("ai session analyzer blocked %q (%s risk): %s",
		tool, decision.RiskLevel, decision.Title)
	if decision.Explanation != "" {
		reason += " — " + decision.Explanation
	}
	return inspect.Denyf(m.Env, jsonrpc.CodePolicyDenied, nameMCPAnalyzer, reason)
}

// shipVerdict encodes the verdict and hands it to the gateway. Every analyzed
// call ships one, blocked or not: the session metrics count analyzed requests
// and risk tiers, so dropping the allow verdicts would report only the blocks.
//
// The session id fills the verdict's ConnID. The gateway dedupes on
// (ConnID, Seq) because an HTTP verdict is sticky on a shared response spec and
// repeats across chunks; here each verdict rides its own packet, and one
// analyzer with one monotonic counter serves the whole MCP session, so the pair
// is unique per analyzed call either way.
func (a *mcpAnalyzer) shipVerdict(decision *aianalyzer.Decision, tool string) {
	if a.emitVerdict == nil {
		return
	}
	encoded, err := decision.Verdict(a.seq.Add(1), a.sid).Encode()
	if err != nil {
		log.With("sid", a.sid, "tool", tool).Warnf("failed encoding mcp ai analyzer verdict: %v", err)
		return
	}
	a.emitVerdict(encoded)
}

func (a *mcpAnalyzer) emit(ctx context.Context, s inspect.Session, ev audit.Event) {
	if a.sink == nil {
		return
	}
	ev.Session = s.ID()
	if ev.User == "" {
		ev.User = s.Identity().Subject
	}
	a.sink.Emit(ctx, ev)
}

// mcpAnalyzedArgs caps the arguments blob sent to the model. Truncation is
// marked so the model does not read a severed JSON object as the whole call.
func mcpAnalyzedArgs(args json.RawMessage) []byte {
	if len(args) <= mcpMaxAnalyzedArgsBytes {
		return args
	}
	out := make([]byte, 0, mcpMaxAnalyzedArgsBytes+len("\n...[truncated]"))
	out = append(out, args[:mcpMaxAnalyzedArgsBytes]...)
	return append(out, "\n...[truncated]"...)
}

// insertBeforeAuditTap places the analyzer immediately before the terminal
// audit tap, or at the end when no tap is present.
//
// Ordering is load-bearing in both directions. The tap must stay last, because
// mcpproxy's runPipeline stops at the first non-Allow verdict: appending after
// it would work for allowed calls and silently skip the tap for blocked ones,
// losing exactly the records an operator cares about. And the analyzer must
// come after the deterministic checks so a call the deny-list already refuses
// never costs an LLM round trip.
func insertBeforeAuditTap(pipeline []inspect.Check, extra inspect.Check) []inspect.Check {
	at := len(pipeline)
	if at > 0 && pipeline[at-1].Name() == checks.NameAuditTap {
		at--
	}
	out := make([]inspect.Check, 0, len(pipeline)+1)
	out = append(out, pipeline[:at]...)
	out = append(out, extra)
	return append(out, pipeline[at:]...)
}

// mcpVerdictEmitter returns the callback that ships an encoded analyzer verdict
// to the gateway, through the session's audit sink.
//
// It rides the same MCPProxyConnectionWrite packet the audit events use, tagged
// as a protocol event so the gateway records it rather than forwarding it to
// the MCP client. The verdict travels in the spec under the key the audit
// plugin already reads (spectypes.AIAnalyzerInfoKey), so session metrics — risk
// counts, blocked totals, the worst verdict — populate for MCP sessions with no
// gateway-side change.
//
// The sink, not client.Send. This callback fires from inside Inspect, on the
// goroutine serving a tool call, so sending inline would grab the agent-wide
// send mutex on the hot path — the same reason audit events were moved behind
// the queue. Going through the sink also means the verdict inherits its
// backpressure and its stop semantics instead of racing session cleanup.
//
// A nil sink (a gateway built without one) drops the verdict rather than
// panicking: the analyzer is optional telemetry, not an enforcement path.
func mcpVerdictEmitter(sink *mcpEventSink) func([]byte) {
	if sink == nil {
		return nil
	}
	return sink.shipVerdict
}
