package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func human(groups ...string) *LevelApprover {
	return &LevelApprover{Type: analyzer.ApproverHuman, Groups: groups}
}

func agent(prompt string) *LevelApprover {
	return &LevelApprover{Type: analyzer.ApproverAIAgent, Prompt: prompt}
}

// approverBlock holds high and medium, with the approvers given.
func approverBlock(a LevelApprovers) *LaneAnalyzerConfig {
	la := laneBlock()
	la.HighRisk, la.MediumRisk = "require_approval", "require_approval"
	la.Approvers = &a
	return la
}

// The approvers block decodes beside the string levels, strictly, and maps
// each held level onto its approver.
func TestApproversDecode(t *testing.T) {
	doc := `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
		"analyzer":{"high":"require_approval","medium":"require_approval","low":"require_approval",
			"approval_rule":"dba-rule",
			"approvers":{"high":{"type":"human","groups":["dba"]},"medium":{"type":"ai_agent","prompt":"p"}}}}]}`
	cfg, err := LoadConfigBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	la := cfg.Listeners[0].Analyzer
	if la.HighRisk != "require_review" || la.LowRisk != "require_review" {
		t.Errorf("levels = %q/%q", la.HighRisk, la.LowRisk)
	}
	if a := la.LevelApprover(analyzer.RiskHigh); a == nil || a.Type != analyzer.ApproverHuman ||
		!slices.Equal(a.Groups, []string{"dba"}) {
		t.Errorf("high approver = %+v", a)
	}
	if a := la.LevelApprover(analyzer.RiskLow); a != nil {
		t.Errorf("low approver = %+v, want none", a)
	}
	want := map[analyzer.RiskLevel]analyzer.Approval{
		analyzer.RiskHigh:   {Approver: analyzer.ApproverHuman},
		analyzer.RiskMedium: {Approver: analyzer.ApproverAIAgent, Prompt: "p"},
	}
	if got := la.approvals(); len(got) != 2 || got[analyzer.RiskHigh] != want[analyzer.RiskHigh] ||
		got[analyzer.RiskMedium] != want[analyzer.RiskMedium] {
		t.Errorf("approvals = %+v", got)
	}
	if !humanHolds(la) || !ruleHolds(la) {
		t.Errorf("humanHolds=%t ruleHolds=%t, want both", humanHolds(la), ruleHolds(la))
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}

	// The served bytes keep the strings and the block.
	raw, err := json.Marshal(ServedForm(*cfg).Listeners[0].Analyzer)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"high":"require_review"`, `"approvers":{"high":{"type":"human","groups":["dba"]}`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("served %s, want %s", raw, want)
		}
	}

	// An unknown key inside an entry is refused like any other.
	bad := strings.Replace(doc, `"prompt":"p"`, `"prompt":"p","approval_groups":["x"]`, 1)
	if _, err := LoadConfigBytes([]byte(bad)); err == nil || !strings.Contains(err.Error(), "approval_groups") {
		t.Errorf("an unknown approvers key loaded: %v", err)
	}
}

// Every approvers entry the sidecar cannot act on, or that nobody would
// read, is refused at load and by the plane's validation.
func TestApproversRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		la   *LaneAnalyzerConfig
		want string
	}{
		"unknown type": {approverBlock(LevelApprovers{High: &LevelApprover{Type: "robot"}}), `approvers.high: unknown type "robot"`},
		"empty type":   {approverBlock(LevelApprovers{High: &LevelApprover{Groups: []string{"dba"}}}), "approvers.high: type is required"},
		"groups on agent": {approverBlock(LevelApprovers{Medium: &LevelApprover{Type: analyzer.ApproverAIAgent,
			Groups: []string{"dba"}}}), "approvers.medium: groups name people nobody asks"},
		"prompt on human": {approverBlock(LevelApprovers{High: &LevelApprover{Type: analyzer.ApproverHuman,
			Prompt: "p"}}), "approvers.high: prompt instructs the approval agent"},
		"blank group": {approverBlock(LevelApprovers{High: human("dba", " ")}), "approvers.high: groups[1] is blank"},
		"level that does not hold": {approverBlock(LevelApprovers{Low: agent("")}),
			`approvers.low names an approver, and the low risk action "" holds nothing`},
	} {
		t.Run(name, func(t *testing.T) {
			tc.la.ApprovalRule = "dba-rule"
			if got := strings.Join(ValidateLaneAnalyzerBlock(tc.la, "appdb"), "; "); !strings.Contains(got, tc.want) {
				t.Errorf("ValidateLaneAnalyzerBlock = %q, want %q", got, tc.want)
			}
			cfg := blockLane(tc.la)
			cfg.ControlPlaneURL = "https://cp.example.com"
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want %q", err, tc.want)
			}
		})
	}

	// A block whose every held level names its approver needs no rule; a
	// held level with none still does.
	ok := approverBlock(LevelApprovers{High: human("dba"), Medium: agent("")})
	if got := ValidateLaneAnalyzerBlock(ok, "appdb"); len(got) != 0 {
		t.Errorf("a block with every approver was refused: %v", got)
	}
	bare := approverBlock(LevelApprovers{High: human("dba")})
	if got := strings.Join(ValidateLaneAnalyzerBlock(bare, "appdb"), "; "); !strings.Contains(got, "names no approval_rule") {
		t.Errorf("a bare medium hold without a rule = %q", got)
	}
	// The rule beside approvers is what an older build files under.
	ok.ApprovalRule = "dba-rule"
	if got := ValidateLaneAnalyzerBlock(ok, "appdb"); len(got) != 0 {
		t.Errorf("approval_rule beside approvers was refused: %v", got)
	}
	// An agent-only lane waits for nobody: review_mode is read by nobody.
	agentOnly := approverBlock(LevelApprovers{High: agent(""), Medium: agent("")})
	agentOnly.ReviewMode = analyzer.ReviewReturn
	if got := strings.Join(ValidateLaneAnalyzerBlock(agentOnly, "appdb"), "; "); !strings.Contains(got, "review_mode") {
		t.Errorf("review_mode on an agent-only lane = %q", got)
	}
}

// ServeLevelApprovers serves a build that reads approvers as is. For an
// older build it drops approvers where an approval_rule can take the held
// levels, and keeps them where none can, so CheckServable refuses.
func TestServeLevelApprovers(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityLevelApprovers) {
		t.Fatalf("the header lacks %s", CapabilityLevelApprovers)
	}
	current := Handshake{Version: "1.300.0", Capabilities: SidecarCapabilities()}
	old := Handshake{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}}

	withRule := approverBlock(LevelApprovers{High: human("dba"), Medium: agent("p")})
	withRule.ApprovalRule = "dba-rule"
	in := *blockLane(withRule)

	if out := ServeLevelApprovers(in, current); out.Listeners[0].Analyzer != in.Listeners[0].Analyzer {
		t.Error("a build with the capability was served a copy")
	}
	if err := CheckServable(in, current); err != nil {
		t.Errorf("a current build was refused: %v", err)
	}

	out := ServeLevelApprovers(in, old)
	if out.Listeners[0].Analyzer.Approvers != nil || out.Listeners[0].Analyzer.ApprovalRule != "dba-rule" ||
		out.Listeners[0].Analyzer.MediumRisk != "require_approval" {
		t.Errorf("served %+v, want the holds under approval_rule", out.Listeners[0].Analyzer)
	}
	if err := CheckServable(out, old); err != nil {
		t.Errorf("the dropped document is not servable: %v", err)
	}
	if in.Listeners[0].Analyzer.Approvers == nil {
		t.Error("ServeLevelApprovers changed its input")
	}

	noRule := *blockLane(approverBlock(LevelApprovers{High: human("dba"), Medium: agent("")}))
	kept := ServeLevelApprovers(noRule, old)
	if kept.Listeners[0].Analyzer.Approvers == nil {
		t.Fatal("approvers were dropped from a block with no approval_rule")
	}
	if err := CheckServable(kept, old); err == nil || !strings.Contains(err.Error(), CapabilityLevelApprovers) {
		t.Errorf("CheckServable = %v, want a refusal naming %s", err, CapabilityLevelApprovers)
	}

	// An empty approvers block says nothing, and is not served.
	empty := laneBlock()
	empty.Approvers = &LevelApprovers{}
	if err := CheckServable(*blockLane(empty), old); err != nil {
		t.Errorf("an empty approvers block was refused: %v", err)
	}
}

// agentStub rates everything high and answers the approval agent.
type agentStub struct {
	highRiskProvider
	tool, args string
	decides    *atomic.Int32
}

func (a agentStub) Decide(context.Context, string, string, []analyzer.ToolSpec) ([]analyzer.ToolCall, error) {
	if a.decides != nil {
		a.decides.Add(1)
	}
	return []analyzer.ToolCall{{Name: a.tool, Args: json.RawMessage(a.args)}}, nil
}

func approving() agentStub {
	return agentStub{tool: "approve", args: `{"reason_code":"in_scope","reason":"bounded"}`, decides: new(atomic.Int32)}
}

// filingPlane records the JSON body of every filing.
func filingPlane(t *testing.T, status int, answer string) (*controlPlane, *[]map[string]any) {
	t.Helper()
	bodies := &[]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*bodies = append(*bodies, body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	return &controlPlane{url: srv.URL, cred: tokenCredential("t")}, bodies
}

func deleteFromUsers() inspect.Statement {
	return inspect.Statement{Protocol: inspect.Postgres, Direction: inspect.FromClient,
		Text: "DELETE FROM users", Operation: inspect.OpDelete, Tables: []string{"users"}}
}

// A human level files its risk level and no approval_rule; a held level with
// no approver files the body it always sent. Neither sends a decision.
func TestTheFilingBodyByLevel(t *testing.T) {
	const answer = `{"forward":false,"review":{"id":"r1","status":"REJECTED"}}`
	for name, tc := range map[string]struct {
		approvers *LevelApprovers
		want      map[string]any
		absent    []string
	}{
		"human level": {&LevelApprovers{High: human("dba")},
			map[string]any{"listener_name": "payments", "risk_level": "high"}, []string{"approval_rule"}},
		"bare hold": {nil,
			map[string]any{"listener_name": "payments", "approval_rule": "payments-approvers"}, []string{"risk_level"}},
	} {
		t.Run(name, func(t *testing.T) {
			cp, bodies := filingPlane(t, http.StatusCreated, answer)
			deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: highRiskProvider{}, cp: cp}
			la := laneBlock()
			la.HighRisk, la.ApprovalRule, la.Approvers = "require_approval", "payments-approvers", tc.approvers
			pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
			if err != nil {
				t.Fatal(err)
			}
			if v := pol.Evaluate(deleteFromUsers()); !v.Denied || len(*bodies) != 1 {
				t.Fatalf("verdict %+v, %d filings", v, len(*bodies))
			}
			body := (*bodies)[0]
			for k, want := range tc.want {
				if body[k] != want {
					t.Errorf("%s = %v, want %v (%v)", k, body[k], want, body)
				}
			}
			payload, _ := base64.StdEncoding.DecodeString(body["payload"].(string))
			if string(payload) != "DELETE FROM users" {
				t.Errorf("payload = %q", payload)
			}
			for _, k := range append(tc.absent, "approver", "decision", "reason") {
				if _, ok := body[k]; ok {
					t.Errorf("body sent %s: %v", k, body)
				}
			}
		})
	}
}

// The approval agent decides in the sidecar: nothing is filed, with or
// without a plane, and no plane or terminal is needed to build the lane.
func TestAnAgentLevelFilesNothing(t *testing.T) {
	cp, bodies := filingPlane(t, http.StatusCreated, `{}`)
	stub := approving()
	deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: stub, cp: cp}
	la := approverBlock(LevelApprovers{High: agent("")})
	la.MediumRisk = ""
	if r := deps.reviewerFor("payments", la, false); r != nil {
		t.Errorf("an agent-only lane got a reviewer: %T", r)
	}
	pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
	if err != nil {
		t.Fatal(err)
	}
	v := pol.Evaluate(deleteFromUsers())
	if v.Denied || len(*bodies) != 0 || stub.decides.Load() != 1 {
		t.Fatalf("denied=%t (%s), %d filings, %d decides", v.Denied, v.Message, len(*bodies), stub.decides.Load())
	}
	if v.Annotations[analyzer.MetadataApprovalDecision] != "approved" ||
		v.Annotations[analyzer.MetadataApprovalReasonCode] != "in_scope" {
		t.Errorf("annotations = %v", v.Annotations)
	}

	// No plane, no terminal: the agent level still builds; a human one
	// does not.
	local := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: approving()}
	agentOnly := approverBlock(LevelApprovers{High: agent(""), Medium: agent("")})
	if _, err := buildLanes(blockLane(agentOnly), nil, local); err != nil {
		t.Fatalf("an agent level without a plane was refused: %v", err)
	}
	humanOnly := approverBlock(LevelApprovers{High: human(), Medium: human()})
	if _, err := buildLanes(blockLane(humanOnly), nil, local); err == nil || !strings.Contains(err.Error(), "in a terminal") {
		t.Fatalf("a human level with nowhere to file built: %v", err)
	}
	withPlane := blockLane(humanOnly)
	withPlane.ControlPlaneURL = "https://cp.example.com"
	if _, err := buildLanes(withPlane, nil, local); err != nil {
		t.Fatalf("a human level under a plane needed an approval_rule: %v", err)
	}

	// The provider must run the agent's tools.
	local.provider = highRiskProvider{}
	if _, err := buildLanes(blockLane(agentOnly), nil, local); err == nil || !strings.Contains(err.Error(), "approval agent") {
		t.Fatalf("an agent level built on a provider without tool calls: %v", err)
	}
}

// The record redactor uses the detector whatever the send mode; with no
// detector the record keeps the code only.
func TestTheAgentReasonIsRedactedForTheRecord(t *testing.T) {
	stub := agentStub{tool: "reject", args: `{"reason_code":"unbounded_scope","reason":"no WHERE on 4111111111111111"}`}
	for name, tc := range map[string]struct {
		det    Plugin
		reason string
		kept   bool
	}{
		"detector, send raw": {stubPlugin{entities: []string{"CREDIT_CARD"}, find: "4111111111111111"},
			"no WHERE on <CREDIT_CARD>", true},
		"no detector": {nil, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: stub, det: tc.det}
			la := approverBlock(LevelApprovers{High: agent(""), Medium: agent("")})
			la.Send = SendRaw
			pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
			if err != nil {
				t.Fatal(err)
			}
			v := pol.Evaluate(deleteFromUsers())
			if !v.Denied || strings.Contains(v.Message, "4111") || !strings.Contains(v.Message, "(unbounded_scope)") {
				t.Fatalf("denied=%t message=%q", v.Denied, v.Message)
			}
			got, kept := v.Annotations[analyzer.MetadataApprovalReason]
			if kept != tc.kept || got != tc.reason {
				t.Errorf("approval_reason = %q (%t), want %q (%t)", got, kept, tc.reason, tc.kept)
			}
			if v.Annotations[analyzer.MetadataApprovalReasonCode] != "unbounded_scope" {
				t.Errorf("annotations = %v", v.Annotations)
			}
		})
	}
}

// An observing lane does not ask the agent, and records would_deny.
func TestAnObservingLaneDoesNotAskTheAgent(t *testing.T) {
	stub := approving()
	deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: stub}
	la := approverBlock(LevelApprovers{High: agent(""), Medium: agent("")})
	pol, err := buildPolicy("payments", GuardrailsConfig{Mode: ModeObserve}, la, nil, nil, deps)
	if err != nil {
		t.Fatal(err)
	}
	v := pol.Evaluate(deleteFromUsers())
	if v.Denied || !strings.Contains(v.Message, "not consulted in observe mode") {
		t.Fatalf("denied=%t message=%q", v.Denied, v.Message)
	}
	if n := stub.decides.Load(); n != 0 {
		t.Errorf("the agent was asked %d time(s) on an observing lane", n)
	}
}

// A local file is told the require_review spelling and approval_rule are
// deprecated, pointing at approvers; a document the plane served never is.
func TestApprovalSpellingsAreDeprecatedInALocalFile(t *testing.T) {
	t.Setenv(ControlPlaneURLEnv, "")
	t.Setenv(SidecarTokenEnv, "")
	doc := `{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1","upstream":"h:1",
		"analyzer":{"high":"require_review","medium":"require_approval","approval_rule":"dba"}}]}`
	loaded, err := LoadConfigBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Deprecations) != 0 {
		t.Errorf("a loaded document warned before anyone knew it is a file: %v", loaded.Deprecations)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := SetupWith(path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(cfg.Deprecations, "\n")
	for _, want := range []string{"high risk: require_review is deprecated",
		"approval_rule is deprecated; write each held level as approvers.<level>: {type: human, groups: [...]}"} {
		if !strings.Contains(got, want) {
			t.Errorf("deprecations %q lack %q", got, want)
		}
	}
	if strings.Contains(got, "medium risk") {
		t.Errorf("require_approval was called deprecated: %q", got)
	}
}

// -migrate writes the current spelling and names each approval_rule it
// cannot convert.
func TestMigrateReportsApprovalRules(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"listeners":[{"name":"appdb","protocol":"postgres","listen":":1",
		"upstream":"h:1","analyzer":{"high":"require_review","approval_rule":"dba"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var out, report strings.Builder
	if err := WriteMigrated(cfg, false, &out, &report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), `appdb: approval_rule "dba" is deprecated`) ||
		!strings.Contains(report.String(), "approvers.<level>: {type: human") {
		t.Errorf("report:\n%s", report.String())
	}
	if !strings.Contains(out.String(), `"high": "require_approval"`) || strings.Contains(out.String(), "require_review") {
		t.Errorf("document:\n%s", out.String())
	}
}
