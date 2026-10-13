package sidecartui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestReviewsURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://cp.example.com":           "https://cp.example.com/reviews",
		"https://cp.example.com/":          "https://cp.example.com/reviews",
		"https://cp.example.com/api":       "https://cp.example.com/reviews",
		"https://hoop.example.com/cp/api/": "https://hoop.example.com/cp/reviews",
		"http://127.0.0.1:8009":            "http://127.0.0.1:8009/reviews",
	} {
		if got := reviewsURL(in); got != want {
			t.Errorf("reviewsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func cpModel(plane string) model {
	now := time.Date(2026, 10, 5, 12, 0, 20, 0, time.UTC)
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	feed(m.st, script)
	m.controlPlane = plane
	m.width, m.height = 140, 40
	return m
}

// Connected to a control plane, Approvals is dimmed in the menu and asks
// nothing of the person: no pending count, no shimmer.
func TestApprovalsAreDimmedUnderAControlPlane(t *testing.T) {
	m := cpModel("https://cp.example.com")
	m.st.ApplyLocalReview(LocalReview{ID: "r1", Listener: "pg", Statement: "DELETE FROM t",
		Status: statusPending, Filed: m.now()})
	if m.st.PendingReviews() == 0 {
		t.Fatal("the test review is not pending")
	}
	out := ansi.Strip(m.render())
	if strings.Contains(out, "awaiting approval") {
		t.Errorf("the header asks for approvals the control plane decides:\n%s", out)
	}
	if !strings.Contains(out, "3 Approvals") || !strings.Contains(out, "↗") {
		t.Errorf("the menu does not mark Approvals as elsewhere:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "3 Approvals") && strings.ContainsAny(strings.TrimSpace(strings.SplitN(l, "3 Approvals", 2)[1])[:1], "0123456789") {
			t.Errorf("Approvals still shows a pending count: %q", l)
		}
	}
}

// Its section names the plane, says decisions happen there, and gives the
// link; o opens it and c copies it.
func TestApprovalsSectionPointsAtTheControlPlane(t *testing.T) {
	m := cpModel("https://cp.example.com/api")
	var opened string
	m.openURL = func(u string) error { opened = u; return nil }
	m.tab, m.menuFocus = tabReviews, false
	out := ansi.Strip(m.render())
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"Approvals are managed by the control plane", "cp.example.com",
		"this terminal cannot decide them", "https://cp.example.com/reviews"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the section lacks %q:\n%s", want, out)
		}
	}
	var tm tea.Model = m
	tm, _ = tm.Update(key("o"))
	if opened != "https://cp.example.com/reviews" {
		t.Errorf("o opened %q", opened)
	}
	if _, cmd := tm.Update(key("c")); cmd == nil {
		t.Error("c did not copy the link")
	}
}

// A sidecar on its own keeps its Approvals as they were.
func TestApprovalsAreUnchangedWithoutAControlPlane(t *testing.T) {
	m := cpModel("")
	m.tab, m.menuFocus = tabReviews, false
	if out := ansi.Strip(m.render()); strings.Contains(out, "managed by the control plane") || strings.Contains(out, "↗") {
		t.Errorf("a standalone sidecar talks about a control plane:\n%s", out)
	}
}
