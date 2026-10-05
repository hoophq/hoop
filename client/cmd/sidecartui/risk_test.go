package sidecartui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// The analyzer's verdict travels with the approval: the risk level shows in
// the list, and the dialog shows the level, the model's title and its
// explanation beside the statement.
func TestApprovalsShowTheAnalyzerVerdict(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	r := NewReviewer()
	_, err := r.file("pg", "delete from users", analyzer.HoldDetail{
		Rule: "pg", RiskLevel: analyzer.RiskHigh,
		Title: "deletes every user", Explanation: "no WHERE clause, so the whole table goes",
	})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	m.reviewer, m.operator = r, "alice"
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 36})
	tm, _ = tm.Update(localReviewsMsg(r.Snapshot()))
	tm, _ = tm.Update(key("3"))

	list := ansi.Strip(tm.(model).render())
	for _, want := range []string{"▲high", "deletes every user", "no WHERE clause"} {
		if !strings.Contains(list, want) {
			t.Errorf("the Approvals section does not show %q", want)
		}
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	dialog := ansi.Strip(tm.(model).render())
	for _, want := range []string{"Approval needed", "▲high", "deletes every user", "no WHERE clause"} {
		if !strings.Contains(dialog, want) {
			t.Errorf("the dialog does not show %q", want)
		}
	}
}
