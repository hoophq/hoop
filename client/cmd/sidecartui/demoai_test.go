package sidecartui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	"github.com/hoophq/hoop/sidecar/analyzer"
)

// Both demos boot with the AI analyzer on, on the model Hoop hosts: no key
// file, temperature 0, the demo's guidance, and high risk held for approval.
func TestDemoDraftsTurnTheHostedAnalyzerOn(t *testing.T) {
	for id, kind := range map[string]sidecardemo.Kind{demoPGID: sidecardemo.KindPostgres, demoHTTPID: sidecardemo.KindHTTP} {
		d, err := newDraft(id, noMachine())
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := d.config()
		if err != nil {
			t.Fatal(err)
		}
		a, la := cfg.Analyzer, cfg.Listeners[0].Analyzer
		if a == nil || la == nil {
			t.Fatalf("%s: the analyzer is off", id)
		}
		if a.UseHoopLLMProvider == nil || !*a.UseHoopLLMProvider || a.Provider != "" || a.CredentialsFile != "" {
			t.Errorf("%s: analyzer = %+v, want the hosted model and no key", id, a)
		}
		if a.Temperature == nil || *a.Temperature != 0 {
			t.Errorf("%s: temperature = %v, want 0", id, a.Temperature)
		}
		// Every read goes to the model: with it unreachable the analyzer
		// must give up before the tour's step does.
		if a.TimeoutSec <= 0 || time.Duration(a.TimeoutSec)*time.Second >= quickWait {
			t.Errorf("%s: timeout_sec = %d, want set and under the step's %v", id, a.TimeoutSec, quickWait)
		}
		if la.HighRisk != string(analyzer.ActionRequireApproval) || la.MediumRisk == string(analyzer.ActionRequireApproval) ||
			la.LowRisk != string(analyzer.ActionAllow) {
			t.Errorf("%s: levels = %s/%s/%s, want only high held", id, la.HighRisk, la.MediumRisk, la.LowRisk)
		}
		if la.Prompt != sidecardemo.AnalyzerPrompt(kind) {
			t.Errorf("%s: the lane does not carry the demo guidance", id)
		}
		// Only the AI step's data goes to the model: the lane holds, so a
		// read the model cannot rate is refused, and the other steps must
		// not depend on it.
		tables, resources := sidecardemo.AnalyzerScope(kind)
		if la.Trigger == nil || len(la.Trigger.Operations) != 0 || len(la.Trigger.Any) != 1 ||
			!slices.Equal(la.Trigger.Any[0].Tables, tables) || !slices.Equal(la.Trigger.Any[0].Resources, resources) ||
			len(la.Trigger.Any[0].Operations) == 0 {
			t.Errorf("%s: trigger = %+v, want one item scoped to the AI step's data", id, la.Trigger)
		}
		if err := validateDraft(t, d); err != nil {
			t.Errorf("%s: the demo draft does not validate: %v", id, err)
		}
		if !strings.Contains(d.analyzerSummary(), "Hoop-hosted") {
			t.Errorf("%s: summary = %q", id, d.analyzerSummary())
		}
	}
}

// Outside the demo the hosted model is a provider like the others: choosing
// it writes use_hoop_llm_provider (the daemon refuses provider "hoop"), with
// no key file, and none of the demo's guidance.
func TestHostedProviderWritesUseHoopLLMProvider(t *testing.T) {
	d, err := newDraft("postgres", noMachine())
	if err != nil {
		t.Fatal(err)
	}
	f := analyzerForm(d.an, d.protocol)
	f.byID("on").on, f.byID("provider").text = true, "hoop"
	if d.an, err = analyzerFromForm(f, d.an); err != nil {
		t.Fatalf("the hosted model needs no model or key, got %v", err)
	}
	cfg, _, err := d.config()
	if err != nil {
		t.Fatal(err)
	}
	a, la := cfg.Analyzer, cfg.Listeners[0].Analyzer
	if a.UseHoopLLMProvider == nil || !*a.UseHoopLLMProvider || a.Provider != "" || a.CredentialsFile != "" || a.Model != "" {
		t.Errorf("analyzer = %+v, want use_hoop_llm_provider only", a)
	}
	if a.Temperature != nil || la.Prompt != "" {
		t.Error("a config outside the demo carries the demo's guidance")
	}
	if err := validateDraft(t, d); err != nil {
		t.Errorf("the hosted draft does not validate: %v", err)
	}
}

// The demo's holds are filed at this terminal although a plane is connected
// (daemon.WithLocalReviewerFirst), so Approvals stays here: a count, the
// header's call, and no link to the plane.
func TestDemoApprovalsStayAtTheTerminalUnderAPlane(t *testing.T) {
	m := cpModel("https://cp.example.com")
	m.localApprovals = true
	m.st.ApplyLocalReview(LocalReview{ID: "r1", Listener: "demo", Statement: "SELECT creditcard FROM customers",
		Status: statusPending, Filed: m.now()})
	out := ansi.Strip(m.render())
	if !strings.Contains(out, "awaiting approval") || strings.Contains(out, "↗") {
		t.Errorf("the demo's approval is not asked for here:\n%s", out)
	}
	m.tab, m.menuFocus = tabReviews, false
	if out := ansi.Strip(m.render()); strings.Contains(out, "managed by the control plane") {
		t.Errorf("the demo's Approvals point at the plane:\n%s", out)
	}
}

// While the AI step waits, the tour says so, and enter opens the decision. a
// does not: in the dialog a approves, so a double press would release a
// statement nobody read.
func TestTourEnterOpensTheHeldApproval(t *testing.T) {
	m := cpModel("")
	m.tour = newTour(&DemoOptions{Kind: sidecardemo.KindPostgres})
	m.tab, m.menuFocus = tabTour, false
	step := slices.IndexFunc(m.tour.steps, func(s sidecardemo.Step) bool { return s.Approval })
	if step < 0 {
		t.Fatal("the Postgres tour has no AI step")
	}
	m.tour.cur, m.tour.running = step, step

	var tm tea.Model = m
	tm, _ = tm.Update(localReviewsMsg{{ID: "r1", Listener: "demo", Statement: m.tour.steps[step].SQL,
		Status: statusPending, Filed: m.now()}})
	m = tm.(model)
	if !m.tour.held {
		t.Fatal("a pending review did not mark the tour held")
	}
	if out := ansi.Strip(m.render()); !strings.Contains(out, "Waiting for your approval") {
		t.Errorf("the tour does not say it waits:\n%s", out)
	}
	tm, _ = tm.Update(key("a"))
	if tm.(model).modal != "" {
		t.Error("a opened the decision")
	}
	tm, _ = tm.Update(key("enter"))
	if tm.(model).modal != "r1" {
		t.Errorf("enter opened %q, want the held review", tm.(model).modal)
	}
}

// The analyzer fails open: with the hosted model unreachable the AI step
// answers without a hold. The tour says so and does not tick the step.
func TestAIStepWithoutAHoldFailsLoudly(t *testing.T) {
	tr := newTour(&DemoOptions{Kind: sidecardemo.KindPostgres})
	step := slices.IndexFunc(tr.steps, func(s sidecardemo.Step) bool { return s.Approval })
	tr.cur = step
	tr.run(step)
	tr.apply(tourResult{step: step, status: 200, body: "rows"})
	if tr.done[step] || tr.result.err == nil || !strings.Contains(tr.result.err.Error(), "did not hold") {
		t.Fatalf("an unheld AI step passed: done %v, err %v", tr.done[step], tr.result.err)
	}

	// The model unreachable: the holding lane refuses the read. The tour
	// says why, for this step and for any other, and ticks neither.
	for _, i := range []int{0, step} {
		tr.run(i)
		tr.apply(tourResult{step: i, status: 403,
			body: analyzer.UnavailableMessage + " and this lane holds statements for approval; denying"})
		if tr.done[i] || tr.result.err == nil || !strings.Contains(tr.result.err.Error(), "could not reach the model") {
			t.Fatalf("step %d refused by an unreachable model: done %v, err %v", i, tr.done[i], tr.result.err)
		}
	}

	tr.run(step)
	tr.setHeld(true)
	tr.setHeld(false)
	tr.apply(tourResult{step: step, status: 200, body: "rows"})
	if !tr.done[step] || tr.result.err != nil {
		t.Fatalf("an approved AI step failed: done %v, err %v", tr.done[step], tr.result.err)
	}
}
