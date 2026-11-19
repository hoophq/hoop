package sidecartui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
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
	// The AI step is a GET too: only the analyzer's high verdict ticks it.
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "get", SessionID: "s4", Metadata: map[string]string{"risk_level": "low"}})
	if tr.done[2] {
		t.Fatal("a low-risk GET ticked Ask for approval")
	}
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "get", SessionID: "s5", Metadata: map[string]string{"risk_level": "high"}})
	tr.see(audit.Event{Kind: audit.KindStatement, Operation: "post", SessionID: "s6"})
	for i := 0; i < 4; i++ {
		if !tr.done[i] {
			t.Errorf("step %d (%s) not ticked", i+1, tr.steps[i].Title)
		}
	}
	if tr.done[4] {
		t.Error("the direct request was ticked: the sidecar never sees it")
	}

	// The approved export comes back masked, the way the lane records it:
	// the GET, the server's answer, then the masked reply. That is the AI
	// step, not See masking.
	ex := newTour(&DemoOptions{})
	ex.see(audit.Event{Kind: audit.KindStatement, Operation: "get", SessionID: "e1", Metadata: map[string]string{"risk_level": "high"}})
	ex.see(audit.Event{Kind: audit.KindStatement, Operation: "unknown", SessionID: "e1", Direction: inspect.FromServer})
	ex.see(audit.Event{Kind: "masked", SessionID: "e1", Direction: inspect.FromServer})
	if !ex.done[2] || ex.done[0] {
		t.Errorf("after the approved export: done = %v, want Ask for approval only", ex.done)
	}
}

func TestTourShowsTheAnswerAndWhatTheSidecarDid(t *testing.T) {
	tr := newTour(&DemoOptions{})
	tr.apply(tourResult{step: 0, status: 200, dur: 3 * time.Millisecond,
		body: "[\n  {\"email\": \"[REDACTED:EMAIL_ADDRESS]\"}\n]"})
	out := ansi.Strip(tr.view(90, 40, time.Now()))
	for _, want := range []string{"200 OK", tr.steps[0].Why, "[REDACTED:EMAIL_ADDRESS]", "1 of 5 done", tr.steps[0].Curl(sidecardemo.DefaultPorts)} {
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
