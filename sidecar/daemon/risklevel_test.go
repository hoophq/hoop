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
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func humanLevel(groups ...string) RiskLevel {
	return RiskLevel{Action: "require_approval", Approver: "human", ApprovalGroups: groups}
}

func agentLevel() RiskLevel {
	return RiskLevel{Action: "require_approval", Approver: "ai_agent"}
}

// A level reads as a bare action or as an object, and writes the bare action
// back when that says everything, so an old block serves the bytes it did.
func TestARiskLevelDecodesAndEncodesBothForms(t *testing.T) {
	var la LaneAnalyzerConfig
	if err := json.Unmarshal([]byte(`{"high":"block","medium":{"action":"warn"},
		"low":{"action":"require_approval","approver":"human","approval_groups":["dba"]}}`), &la); err != nil {
		t.Fatal(err)
	}
	if la.HighRisk.Action != "block" || la.MediumRisk.Action != "warn" ||
		la.LowRisk.Approver != "human" || !slices.Equal(la.LowRisk.ApprovalGroups, []string{"dba"}) {
		t.Fatalf("decoded %+v", la)
	}
	raw, err := json.Marshal(la)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"high":"block","medium":"warn","low":{"action":"require_approval","approver":"human","approval_groups":["dba"]}}`
	if string(raw) != want {
		t.Errorf("encoded %s\nwant    %s", raw, want)
	}
	if raw, _ := json.Marshal(LaneAnalyzerConfig{}); string(raw) != "{}" {
		t.Errorf("an empty block serves %s", raw)
	}

	if err := json.Unmarshal([]byte(`{"high":{"action":"block","approvers":"x"}}`), &la); err == nil ||
		!strings.Contains(err.Error(), "approvers") {
		t.Errorf("an unknown key in a level loaded: %v", err)
	}
}

// An object with no action is refused, the empty object included, and stays
// an object when encoded, so the refusal survives a round trip.
func TestAnObjectWithNoActionIsRefused(t *testing.T) {
	for _, level := range []string{`{}`, `{"action":""}`} {
		var la LaneAnalyzerConfig
		if err := json.Unmarshal([]byte(`{"high":"block","medium":`+level+`}`), &la); err != nil {
			t.Fatalf("%s: %v", level, err)
		}
		problems := strings.Join(ValidateLaneAnalyzerBlock(&la, "appdb"), "\n")
		if !strings.Contains(problems, "medium risk is an object with no action") {
			t.Errorf("%s: problems %q, want the missing action refused", level, problems)
		}
		raw, err := json.Marshal(la)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != `{"high":"block","medium":{}}` {
			t.Errorf("%s: encoded %s, want the object kept", level, raw)
		}
	}
}

// An object that holds and names no approver gets ai_agent, the ticket's
// default; a bare hold keeps the lane-wide human approver.
func TestAnObjectHoldDefaultsToTheApprovalAgent(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"listeners":[{"name":"appdb","protocol":"postgres",
		"listen":":1","upstream":"h:1","analyzer":{"high":{"action":"require_approval"},
		"medium":"require_approval","approval_rule":"dba"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	la := cfg.Listeners[0].Analyzer
	if la.HighRisk.Approver != "ai_agent" || la.HighRisk.Action != "require_review" {
		t.Errorf("high = %+v, want require_review with approver ai_agent", la.HighRisk)
	}
	if la.MediumRisk.approver() != "" || la.MediumRisk.Action != "require_review" {
		t.Errorf("medium = %+v, want the bare hold", la.MediumRisk)
	}
	ap := la.approvals()
	if len(ap) != 1 || ap[analyzer.RiskHigh].Approver != analyzer.ApproverAIAgent {
		t.Errorf("approvals = %+v", ap)
	}
}

// The pairing a hold needs depends on its approver: a bare hold needs the
// approval_rule, an object level does not, and the wait settings need a
// level a person approves.
func TestHoldPairingFollowsTheApprover(t *testing.T) {
	for name, tc := range map[string]struct {
		la   LaneAnalyzerConfig
		want string
	}{
		"human object needs no rule":    {la: LaneAnalyzerConfig{HighRisk: humanLevel("dba")}},
		"agent object needs no rule":    {la: LaneAnalyzerConfig{HighRisk: agentLevel()}},
		"bare hold needs the rule":      {la: LaneAnalyzerConfig{HighRisk: RiskLevel{Action: "require_approval"}}, want: "names no approval_rule"},
		"object with no action":         {la: LaneAnalyzerConfig{HighRisk: RiskLevel{Approver: "human"}}, want: "object with no action"},
		"blank group":                   {la: LaneAnalyzerConfig{HighRisk: humanLevel(" ")}, want: "approval_groups[0] is blank"},
		"human level takes review_mode": {la: LaneAnalyzerConfig{HighRisk: humanLevel(), ReviewMode: "return"}},
		"agent level refuses review_mode": {la: LaneAnalyzerConfig{HighRisk: agentLevel(), ReviewMode: "return"},
			want: "with a human approver"},
		"agent level refuses spend_approval_on": {la: LaneAnalyzerConfig{HighRisk: agentLevel(), SpendApprovalOn: SpendOnAccepted},
			want: "with a human approver"},
		"agent level refuses match_ignore": {la: LaneAnalyzerConfig{HighRisk: agentLevel(),
			MatchIgnore: []analyzer.MatchIgnore{{Resources: []string{"/a"}, Fields: [][]string{{"t"}}}}}, want: "with a human approver"},
	} {
		t.Run(name, func(t *testing.T) {
			problems := strings.Join(ValidateLaneAnalyzerBlock(&tc.la, "appdb"), "\n")
			if tc.want == "" && problems != "" {
				t.Fatalf("refused: %s", problems)
			}
			if !strings.Contains(problems, tc.want) {
				t.Fatalf("problems %q, want %q", problems, tc.want)
			}
		})
	}
}

// Each row of the ticket's warning table, plus an approval_rule the object
// levels ignore. Warned, never refused.
func TestEachIgnoredSettingIsWarned(t *testing.T) {
	for name, tc := range map[string]struct {
		la   LaneAnalyzerConfig
		want string
	}{
		"groups on an agent level": {LaneAnalyzerConfig{HighRisk: RiskLevel{Action: "require_approval",
			Approver: "ai_agent", ApprovalGroups: []string{"dba"}}}, "nobody consults"},
		"approver on a level that holds nothing": {LaneAnalyzerConfig{HighRisk: RiskLevel{Action: "block",
			Approver: "human"}}, "holds nothing"},
		"unknown approver": {LaneAnalyzerConfig{HighRisk: RiskLevel{Action: "require_approval",
			Approver: "robot"}}, `approver "robot" is unknown and ignored; the level uses the default approver "ai_agent"`},
		"prompt on a human level": {LaneAnalyzerConfig{HighRisk: RiskLevel{Action: "require_approval",
			Approver: "human", Prompt: "p"}}, "prompt is ignored"},
		"approval_rule beside object holds": {LaneAnalyzerConfig{HighRisk: humanLevel(),
			ApprovalRule: "dba"}, `approval_rule "dba" is ignored`},
	} {
		t.Run(name, func(t *testing.T) {
			if p := ValidateLaneAnalyzerBlock(&tc.la, "appdb"); len(p) != 0 {
				t.Fatalf("refused: %v", p)
			}
			if got := strings.Join(LaneAnalyzerWarnings(&tc.la), "\n"); !strings.Contains(got, tc.want) {
				t.Errorf("warnings %q, want %q", got, tc.want)
			}
		})
	}
	if w := LaneAnalyzerWarnings(&LaneAnalyzerConfig{HighRisk: humanLevel("dba"), MediumRisk: agentLevel()}); len(w) != 0 {
		t.Errorf("a clean block warned: %v", w)
	}
}

// The warnings reach -validate's lane notes, which the start log repeats.
func TestValidateReportsTheWarnings(t *testing.T) {
	la := laneBlock()
	la.HighRisk = RiskLevel{Action: "require_approval", Approver: "human", Prompt: "p"}
	cfg := blockLane(la)
	cfg.ControlPlaneURL = "https://cp.example.com"
	lanes, err := Validate(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lanes[0].Notes, "\n"), "prompt is ignored") {
		t.Errorf("notes %v lack the warning", lanes[0].Notes)
	}
}

// A level the approval agent decides needs no plane and no terminal; a
// level a person approves needs one of them, as the bare hold does.
func TestOnlyAHumanLevelNeedsSomewhereToFile(t *testing.T) {
	deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: agentStub{}}
	agent := blockLane(&LaneAnalyzerConfig{HighRisk: agentLevel()})
	if _, err := buildLanes(agent, nil, deps); err != nil {
		t.Fatalf("an agent level without a plane was refused: %v", err)
	}
	human := blockLane(&LaneAnalyzerConfig{HighRisk: humanLevel()})
	if _, err := buildLanes(human, nil, deps); err == nil || !strings.Contains(err.Error(), "in a terminal") {
		t.Fatalf("a human level with nowhere to file built: %v", err)
	}
	human.ControlPlaneURL = "https://cp.example.com"
	if _, err := buildLanes(human, nil, deps); err != nil {
		t.Fatalf("a human level under a plane needed an approval_rule: %v", err)
	}

	// The provider must run the agent's tools.
	deps.provider = highRiskProvider{}
	if _, err := buildLanes(agent, nil, deps); err == nil || !strings.Contains(err.Error(), "approval agent") {
		t.Fatalf("an agent level built on a provider without tool calls: %v", err)
	}
}

// agentStub rates everything high and approves, as a ToolCaller.
type agentStub struct{ highRiskProvider }

func (agentStub) Decide(context.Context, string, string, []analyzer.ToolSpec) ([]analyzer.ToolCall, error) {
	return []analyzer.ToolCall{{Name: "approve", Args: json.RawMessage(`{"reason":"bounded"}`)}}, nil
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

// A human level files its risk level and approver and no approval_rule; the
// plane authorizes it against the level's groups.
func TestAHumanLevelFilesItsLevel(t *testing.T) {
	cp, bodies := filingPlane(t, http.StatusCreated, `{"forward":false,"review":{"id":"r1","status":"REJECTED"}}`)
	deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: highRiskProvider{}, cp: cp}
	la := laneBlock()
	la.HighRisk = humanLevel("dba")
	pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
	if err != nil {
		t.Fatal(err)
	}
	v := pol.Evaluate(deleteFromUsers())
	if !v.Denied || len(*bodies) != 1 {
		t.Fatalf("verdict %+v, %d filings", v, len(*bodies))
	}
	body := (*bodies)[0]
	if body["risk_level"] != "high" || body["approver"] != "human" || body["listener_name"] != "payments" {
		t.Errorf("body = %v", body)
	}
	for _, k := range []string{"approval_rule", "decision", "reason"} {
		if _, ok := body[k]; ok {
			t.Errorf("a human level sent %s: %v", k, body)
		}
	}
}

// The agent's decision is filed already decided. An approval forwards only
// on a 2xx that names the review and the approval it recorded; a rejection
// denies either way.
func TestTheAgentDecisionIsFiled(t *testing.T) {
	const recorded = `{"forward":false,"review":{"id":"a1","status":"APPROVED"}}`
	for name, tc := range map[string]struct {
		status int
		answer string
		denied bool
	}{
		"plane takes it":         {http.StatusCreated, recorded, false},
		"plane refuses it":       {http.StatusUnprocessableEntity, recorded, true},
		"plane is down 503":      {http.StatusServiceUnavailable, recorded, true},
		"2xx that is not JSON":   {http.StatusCreated, `<html>ok</html>`, true},
		"2xx with no review":     {http.StatusCreated, `{"forward":false}`, true},
		"2xx with no review id":  {http.StatusCreated, `{"forward":false,"review":{"status":"APPROVED"}}`, true},
		"2xx with another state": {http.StatusCreated, `{"forward":false,"review":{"id":"a1","status":"PENDING"}}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			cp, bodies := filingPlane(t, tc.status, tc.answer)
			deps := &analyzerDeps{cfg: &AnalyzerConfig{Provider: "stub", Model: "m"}, provider: agentStub{}, cp: cp}
			la := laneBlock()
			la.HighRisk = agentLevel()
			pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
			if err != nil {
				t.Fatal(err)
			}
			v := pol.Evaluate(deleteFromUsers())
			if v.Denied != tc.denied {
				t.Fatalf("denied = %t (%s), want %t", v.Denied, v.Message, tc.denied)
			}
			body := (*bodies)[0]
			payload, _ := base64.StdEncoding.DecodeString(body["payload"].(string))
			if body["risk_level"] != "high" || body["approver"] != "ai_agent" || body["decision"] != "approved" ||
				body["reason"] != "bounded" || string(payload) != "DELETE FROM users" {
				t.Errorf("body = %v", body)
			}
			if _, ok := body["approval_rule"]; ok {
				t.Errorf("an agent decision named an approval_rule: %v", body)
			}
		})
	}
}

func deleteFromUsers() inspect.Statement {
	return inspect.Statement{Protocol: inspect.Postgres, Direction: inspect.FromClient,
		Text: "DELETE FROM users", Operation: inspect.OpDelete, Tables: []string{"users"}}
}

// The object form is a capability: reported by this build, refused to one
// without it, the ai_agent default included.
func TestTheObjectFormNeedsItsCapability(t *testing.T) {
	if !slices.Contains(SidecarCapabilities(), CapabilityRiskLevelObject) {
		t.Fatalf("the header lacks %s", CapabilityRiskLevelObject)
	}
	old := Handshake{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}}
	cfg := *blockLane(&LaneAnalyzerConfig{HighRisk: humanLevel("dba")})
	if err := CheckServable(cfg, old); err == nil || !strings.Contains(err.Error(), CapabilityRiskLevelObject) {
		t.Errorf("an object level was servable to an older build: %v", err)
	}
	if err := CheckServable(cfg, Handshake{Version: "1.200.0", Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("refused to this build: %v", err)
	}

	var decoded LaneAnalyzerConfig
	if err := json.Unmarshal([]byte(`{"high":{"action":"require_approval"}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if err := CheckServable(*blockLane(&decoded), old); err == nil {
		t.Error("an object hold with the default approver was servable to an older build")
	}
	// A bare level and a non-holding object are what every build reads.
	if err := CheckServable(*blockLane(&LaneAnalyzerConfig{HighRisk: RiskLevel{Action: "block", object: true}}), old); err != nil {
		t.Errorf("a bare level was refused: %v", err)
	}
}

// For an older build, a human level becomes the bare hold under the rule the
// plane names; anything a bare level cannot say is refused by capability.
func TestDowngradeRiskLevels(t *testing.T) {
	old := Handshake{Version: "1.200.0", Capabilities: []string{"protocol:postgres"}}
	rule := func(string) string { return "managed-rule" }
	doc := func(la LaneAnalyzerConfig) Config { return *blockLane(&la) }

	t.Run("a build with the capability gets the document as is", func(t *testing.T) {
		in := doc(LaneAnalyzerConfig{HighRisk: agentLevel()})
		out, err := DowngradeRiskLevels(in, Handshake{Capabilities: SidecarCapabilities()}, rule)
		if err != nil || out.Listeners[0].Analyzer != in.Listeners[0].Analyzer {
			t.Fatalf("changed: %v", err)
		}
	})
	t.Run("human levels become the bare hold under the rule", func(t *testing.T) {
		in := doc(LaneAnalyzerConfig{HighRisk: humanLevel("b", "a"), MediumRisk: humanLevel("a", "b"),
			LowRisk: RiskLevel{Action: "warn", object: true}, ReviewMode: "return"})
		out, err := DowngradeRiskLevels(in, old, rule)
		if err != nil {
			t.Fatal(err)
		}
		la := out.Listeners[0].Analyzer
		raw, _ := json.Marshal(la)
		if string(raw) != `{"high":"require_review","medium":"require_review","low":"warn","approval_rule":"managed-rule","review_mode":"return"}` {
			t.Errorf("served %s", raw)
		}
		if err := CheckServable(out, old); err != nil {
			t.Errorf("the downgrade is not servable: %v", err)
		}
		if in.Listeners[0].Analyzer.HighRisk.Approver != "human" || in.Listeners[0].Analyzer.ApprovalRule != "" {
			t.Error("the downgrade changed its input")
		}
	})
	for name, tc := range map[string]struct {
		la   LaneAnalyzerConfig
		rule func(string) string
		want string
	}{
		"agent level":           {LaneAnalyzerConfig{HighRisk: agentLevel()}, rule, "ai_agent approver"},
		"different groups":      {LaneAnalyzerConfig{HighRisk: humanLevel("a"), MediumRisk: humanLevel("b")}, rule, "different approval_groups"},
		"no rule":               {LaneAnalyzerConfig{HighRisk: humanLevel("a")}, func(string) string { return "" }, "no approval rule"},
		"bare hold, other rule": {LaneAnalyzerConfig{HighRisk: humanLevel("a"), MediumRisk: RiskLevel{Action: "require_review"}, ApprovalRule: "dba"}, rule, `approval_rule "dba"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DowngradeRiskLevels(doc(tc.la), old, tc.rule)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), CapabilityRiskLevelObject) {
				t.Errorf("err = %v, want %q naming the capability", err, tc.want)
			}
		})
	}
}

// A local file is told the require_review spelling and approval_rule are
// deprecated; a document the plane served never is.
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
	for _, want := range []string{"high risk: require_review is deprecated", "approval_rule is deprecated"} {
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
	if !strings.Contains(report.String(), `appdb: approval_rule "dba" is deprecated`) {
		t.Errorf("report:\n%s", report.String())
	}
	if !strings.Contains(out.String(), `"high": "require_approval"`) || strings.Contains(out.String(), "require_review") {
		t.Errorf("document:\n%s", out.String())
	}
}
