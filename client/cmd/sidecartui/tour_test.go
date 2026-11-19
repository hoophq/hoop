package sidecartui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"

	"github.com/hoophq/hoop/sidecar/audit"
)

// A curl from another terminal ticks its step from the audit trail, so the
// guide follows the person whichever way they try it.
func TestTourTicksStepsFromTheAuditTrail(t *testing.T) {
	tr := newTour(&DemoOptions{})
	// The POST's reply is masked too, but that is the write step, not the
	// masking one: a masked event takes its session's last operation.
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "post", SessionID: "s1"})
	tr.see(audit.Event{Kind: "masked", SessionID: "s1"})
	if tr.done[0] {
		t.Fatal("the POST's masked reply ticked See masking")
	}
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "get", SessionID: "s2"})
	if tr.done[0] || tr.done[1] {
		t.Fatal("a plain GET ticked masking or the guardrail")
	}
	tr.see(audit.Event{Kind: "masked", SessionID: "s2"})
	tr.see(audit.Event{Kind: "violation", Operation: "delete", SessionID: "s3"})
	for i := 0; i < 3; i++ {
		if !tr.done[i] {
			t.Errorf("step %d (%s) not ticked", i+1, tr.steps[i].Title)
		}
	}
	if tr.done[3] {
		t.Error("the direct request was ticked: the sidecar never sees it")
	}
}

func TestTourShowsTheAnswerAndWhatTheSidecarDid(t *testing.T) {
	tr := newTour(&DemoOptions{})
	tr.apply(tourResult{step: 0, status: 200, dur: 3 * time.Millisecond,
		body: "[\n  {\"email\": \"[REDACTED:EMAIL_ADDRESS]\"}\n]"})
	out := ansi.Strip(tr.view(90, 40, time.Now()))
	for _, want := range []string{"200 OK", tr.steps[0].Why, "[REDACTED:EMAIL_ADDRESS]", "1 of 4 done", tr.steps[0].Curl(sidecardemo.DefaultPorts)} {
		if !strings.Contains(strings.Join(strings.Fields(out), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("the view lacks %q:\n%s", want, out)
		}
	}
	if got := highlightMasked(`"a": "[REDACTED:X]", "b": "[REDACTED:Y]"`); ansi.Strip(got) != `"a": "[REDACTED:X]", "b": "[REDACTED:Y]"` {
		t.Errorf("highlightMasked changed the text: %q", ansi.Strip(got))
	}
}

func TestTourBrowserKeyOnlyOpensGETs(t *testing.T) {
	var opened []string
	tr := newTour(&DemoOptions{OpenURL: func(u string) error { opened = append(opened, u); return nil }})
	tr.key(key("o"))
	tr.cur = 1 // DELETE
	tr.key(key("o"))
	if len(opened) != 1 || !strings.HasSuffix(opened[0], "/users") {
		t.Errorf("opened = %v, want only the GET step", opened)
	}
	if !strings.Contains(tr.flash, "only send GET") {
		t.Errorf("flash = %q, want it to say why DELETE did not open", tr.flash)
	}
}

// With an analyzer, the tour has a step for it. A rating of medium or high
// on its operation ticks it, a low one does not, and the panel shows the
// level and what it mapped to, read from the audit trail.
func TestTourAnalyzerStep(t *testing.T) {
	if n := len(newTour(&DemoOptions{Kind: sidecardemo.KindPostgres}).steps); n != len(sidecardemo.StepsFor(sidecardemo.KindPostgres, false)) {
		t.Fatalf("no analyzer: %d steps", n)
	}
	tr := newTour(&DemoOptions{Kind: sidecardemo.KindPostgres, Analyzer: true})
	i := -1
	for j, s := range tr.steps {
		if s.Analyzer {
			i = j
		}
	}
	if i < 0 {
		t.Fatal("no analyzer step with an analyzer")
	}
	tr.cur = i
	_ = tr.run(i) // the command is not executed: only the bookkeeping matters
	if !strings.Contains(ansi.Strip(tr.verdictLine()), "waiting") {
		t.Errorf("before the trail answers: %q", ansi.Strip(tr.verdictLine()))
	}
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "update", SessionID: "s1",
		Metadata: map[string]string{metaRiskLevel: "low", metaRiskAction: "allow", metaAIStatus: "classified"}})
	if tr.done[i] {
		t.Fatal("a low rating ticked the analyzer step")
	}
	tr.see(audit.Event{Kind: "violation", Operation: "update", SessionID: "s1", Message: "high risk: no WHERE clause",
		Metadata: map[string]string{metaRiskLevel: "high", metaRiskAction: "block", metaAIStatus: "classified"}})
	if !tr.done[i] {
		t.Fatal("a high rating did not tick the analyzer step")
	}
	line := ansi.Strip(tr.verdictLine())
	for _, want := range []string{"→ block", "no WHERE clause"} {
		if !strings.Contains(line, want) {
			t.Errorf("verdict line %q lacks %q", line, want)
		}
	}
	// No rating at all (the model was not reached) says so.
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "update", SessionID: "s2",
		Metadata: map[string]string{metaAIStatus: "error"}})
	if line := ansi.Strip(tr.verdictLine()); !strings.Contains(line, "no rating") || !strings.Contains(line, "error") {
		t.Errorf("unrated verdict line %q", line)
	}
}

// The setup screen's demo hands the tour both demos: every step of each, under
// one heading per demo, and an event ticks only the steps of the listener
// that recorded it.
func TestTourRunsEveryDemo(t *testing.T) {
	tr := newTour(&DemoOptions{Targets: []DemoTarget{
		{Kind: sidecardemo.KindHTTP, Listener: "demo-api", Analyzer: true},
		{Kind: sidecardemo.KindPostgres, Listener: "demo-postgres", Analyzer: true},
	}})
	want := len(sidecardemo.StepsFor(sidecardemo.KindHTTP, true)) + len(sidecardemo.StepsFor(sidecardemo.KindPostgres, true))
	if len(tr.steps) != want {
		t.Fatalf("%d steps, want %d", len(tr.steps), want)
	}
	view := ansi.Strip(tr.view(120, 80, time.Now()))
	for _, h := range []string{"HTTP · AN INVENTED API", "POSTGRES · HOOP'S DEMO DATABASE"} {
		if !strings.Contains(view, h) {
			t.Errorf("view lacks the heading %q:\n%s", h, view)
		}
	}
	// A masked reply on the API ticks the API's masking step, not the database's.
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "get", SessionID: "s1", Connection: "demo-api"})
	tr.see(audit.Event{Kind: "masked", SessionID: "s1", Connection: "demo-api"})
	for i, s := range tr.steps {
		if s.Title != "See masking" {
			continue
		}
		if wantDone := !tr.pgAt(i); tr.done[i] != wantDone {
			t.Errorf("step %d (%s, pg %v): done %v, want %v", i, s.Title, tr.pgAt(i), tr.done[i], wantDone)
		}
	}
	// Each step copies its own demo's command.
	for i := range tr.steps {
		if c := tr.command(i); tr.pgAt(i) != strings.Contains(c, "psql") {
			t.Errorf("step %d copies %q", i, c)
		}
	}
}
