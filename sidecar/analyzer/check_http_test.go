package analyzer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

func httpDelete(target string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Text:      "DELETE " + target,
		Operation: inspect.OpDelete,
		Tables:    []string{"/orders/*"},
		HTTP:      &inspect.HTTPDetail{Method: "DELETE", Path: target, Target: target, Resource: "/orders/*"},
	}
}

func order(status string) *fakeChecker {
	return &fakeChecker{ready: true, target: &inspect.TargetFacts{
		Status: 200, ContentType: "application/json", Body: `{"id":1,"status":"` + status + `"}`,
	}}
}

func TestAnHTTPRequestCarriesWhatTheGETReturned(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskLow}
	var events []analyzer.ResourceCheckEvent
	ev := checkEvaluator(t, p, &events)
	chk := order("open")

	ev.EvaluateWith(httpDelete("/orders/1"), &policy.EvalContext{ResourceCheck: chk})

	got := lastContent(p)
	want := "DELETE /orders/1\nResource: /orders/*\n\n" +
		"Resource check (a GET of the same URL, sent by the sidecar before the request runs):\n" +
		"Status: 200 OK\nContent-Type: application/json\nBody:\n{\"id\":1,\"status\":\"open\"}"
	if got != want {
		t.Errorf("model input:\n%s\nwant:\n%s", got, want)
	}
	if w := chk.checks(); len(w) != 1 || !w[0].Target || len(w[0].Tables) != 0 || w[0].Plan || w[0].Timeout != analyzer.CheckTimeout {
		t.Errorf("checks = %+v", w)
	}
	if prompt, _ := p.seenPrompt.Load().(string); !strings.Contains(prompt, "From an HTTP API") {
		t.Error("the system prompt does not explain the HTTP block")
	}
	if len(events) != 1 || !events[0].Target {
		t.Errorf("events = %+v", events)
	}
}

func TestTheSameHTTPRequestOnTwoResourcesIsClassifiedTwice(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskLow}
	ev := checkEvaluator(t, p, nil)
	ev.EvaluateWith(httpDelete("/orders/1"), &policy.EvalContext{ResourceCheck: order("open")})
	ev.EvaluateWith(httpDelete("/orders/2"), &policy.EvalContext{ResourceCheck: order("closed")})
	ev.EvaluateWith(httpDelete("/orders/3"), &policy.EvalContext{ResourceCheck: order("closed")})
	if n := p.calls.Load(); n != 2 {
		t.Fatalf("calls = %d, want one per resource state", n)
	}
}

func TestTheHTTPBlock(t *testing.T) {
	long := `{"notes":"` + strings.Repeat("x", 4000) + `"}`
	for _, tc := range []struct {
		name  string
		facts inspect.TargetFacts
		want  []string
		not   []string
	}{
		{"not found", inspect.TargetFacts{Status: 404}, []string{"Status: 404 Not Found"}, []string{"Body"}},
		{"cut by the codec", inspect.TargetFacts{Status: 200, Body: `{"a":1`, Truncated: true}, []string{"Body:\n{\"a\":1\n(truncated)"}, nil},
		{"over the block cap", inspect.TargetFacts{Status: 200, Body: long}, []string{"...[truncated]"}, nil},
		{"not read", inspect.TargetFacts{Status: 200, BodyErr: "the body is encoded as gzip"}, []string{"Body not read: the body is encoded as gzip"}, nil},
		{"one-line type", inspect.TargetFacts{Status: 200, ContentType: "text/plain\nStatus: 404"}, []string{"Content-Type: text/plain Status: 404"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &stubProvider{level: analyzer.RiskLow}
			facts := tc.facts
			checkEvaluator(t, p, nil).EvaluateWith(httpDelete("/orders/1"),
				&policy.EvalContext{ResourceCheck: &fakeChecker{ready: true, target: &facts}})
			got := lastContent(p)
			block := got[strings.Index(got, "Resource check"):]
			if len(block) > analyzer.MaxCheckBlockBytes {
				t.Errorf("block is %d bytes", len(block))
			}
			for _, w := range tc.want {
				if !strings.Contains(block, w) {
					t.Errorf("block lacks %q:\n%s", w, block)
				}
			}
			for _, w := range tc.not {
				if strings.Contains(block, w) {
					t.Errorf("block has %q:\n%s", w, block)
				}
			}
		})
	}
}

func TestAnHTTPCheckRunsOnlyWhereItCan(t *testing.T) {
	for _, tc := range []struct {
		name string
		chk  func(*fakeChecker)
		cfg  func(*analyzer.Config)
	}{
		{"skip hook", func(f *fakeChecker) { f.skip = true }, nil},
		{"budget spent", nil, func(c *analyzer.Config) { c.MaxCalls = 1; c.Budget = spentBudget(t) }},
		{"flag off", nil, func(c *analyzer.Config) { c.ResourceCheck = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &stubProvider{level: analyzer.RiskLow}
			cfg := analyzer.Config{Provider: p, Trigger: analyzer.Trigger{All: true}, ResourceCheck: true}
			if tc.cfg != nil {
				tc.cfg(&cfg)
			}
			chk := order("open")
			if tc.chk != nil {
				tc.chk(chk)
			}
			mustNew(t, cfg).EvaluateWith(httpDelete("/orders/1"), &policy.EvalContext{ResourceCheck: chk})
			if n := len(chk.checks()); n != 0 {
				t.Fatalf("checked %d times", n)
			}
		})
	}
}

func TestTheHTTPBlockGoesThroughTheRedactor(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskLow}
	ev := mustNew(t, analyzer.Config{Provider: p, Trigger: analyzer.Trigger{All: true}, ResourceCheck: true,
		Redact: func(s string) string { return strings.ReplaceAll(s, "alice@example.com", "<EMAIL>") }})
	chk := &fakeChecker{ready: true, target: &inspect.TargetFacts{Status: 200, Body: `{"owner":"alice@example.com"}`}}
	ev.EvaluateWith(httpDelete("/orders/1"), &policy.EvalContext{ResourceCheck: chk})
	if got := lastContent(p); strings.Contains(got, "alice@example.com") || !strings.Contains(got, `{"owner":"<EMAIL>"}`) {
		t.Fatalf("model input:\n%s", got)
	}
}
