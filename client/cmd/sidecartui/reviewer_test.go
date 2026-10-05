package sidecartui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// The analyzer's hold loop is written against the control plane's contract;
// these pin the local reviewer to it.
func TestReviewerReleasesAnApprovalOnce(t *testing.T) {
	r := NewReviewer()
	lane := r.For("pg")

	res, err := lane.File(context.Background(), "delete from users")
	if err != nil || res.Status != statusPending || res.ID == "" || res.Forward {
		t.Fatalf("File = %+v, %v; want a pending review", res, err)
	}
	select {
	case <-r.Changed():
	default:
		t.Fatal("filing raised no change for the terminal")
	}
	if snap := r.Snapshot(); len(snap) != 1 || snap[0].Status != statusPending {
		t.Fatalf("snapshot after filing = %+v", snap)
	}
	// Nobody reads Changed here, and filing more must not wait for them:
	// File runs inside a client's statement.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			_, _ = lane.File(context.Background(), fmt.Sprintf("delete from t%d", i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("File blocked on a terminal that was not reading")
	}

	// A resend of the same bytes answers from the same review.
	again, _ := lane.File(context.Background(), "delete from users")
	if again.ID != res.ID || again.Status != statusPending {
		t.Fatalf("resend = %+v, want the pending review %s", again, res.ID)
	}
	if c, _ := lane.Claim(context.Background(), res.ID); c.Forward || c.Status != statusPending {
		t.Fatalf("claim before a decision = %+v", c)
	}

	if _, ok := r.Decide(res.ID, true, "alice"); !ok {
		t.Fatal("Decide refused a pending review")
	}
	if _, ok := r.Decide(res.ID, false, "alice"); ok {
		t.Fatal("a second decision changed a settled review")
	}

	first, _ := lane.Claim(context.Background(), res.ID)
	if !first.Forward {
		t.Fatalf("the claim after approval did not release: %+v", first)
	}
	second, _ := lane.Claim(context.Background(), res.ID)
	if second.Forward || second.Status != statusExecuted {
		t.Fatalf("an approval released twice: %+v", second)
	}

	// The approval is spent: the same statement again needs a new review.
	fresh, _ := lane.File(context.Background(), "delete from users")
	if fresh.ID == res.ID || fresh.Status != statusPending {
		t.Fatalf("a resend after the release reused the spent review: %+v", fresh)
	}
}

// Review mode "return": the client is denied with the id and resends the
// identical statement; the resend is what spends the approval.
func TestReviewerReleasesAResendAfterApproval(t *testing.T) {
	r := NewReviewer()
	lane := r.For("pg")
	res, _ := lane.File(context.Background(), "drop table t")
	r.Decide(res.ID, true, "alice")
	got, _ := lane.File(context.Background(), "drop table t")
	if !got.Forward || got.ID != res.ID {
		t.Fatalf("the resend after approval = %+v, want released under %s", got, res.ID)
	}
	// Another lane with the same bytes is another statement.
	other, _ := r.For("mysql").File(context.Background(), "drop table t")
	if other.Forward || other.ID == res.ID {
		t.Fatalf("an approval on pg released a statement on mysql: %+v", other)
	}
}

func TestReviewerRejectionIsFinal(t *testing.T) {
	r := NewReviewer()
	lane := r.For("pg")
	res, _ := lane.File(context.Background(), "truncate t")
	r.Decide(res.ID, false, "alice")
	if c, _ := lane.Claim(context.Background(), res.ID); c.Forward || c.Status != statusRejected {
		t.Fatalf("claim after rejection = %+v", c)
	}
	// A resend files a new review, the plane's rule: the person sees it
	// again rather than the statement being refused forever.
	again, _ := lane.File(context.Background(), "truncate t")
	if again.ID == res.ID || again.Status != statusPending {
		t.Fatalf("resend after rejection = %+v", again)
	}
	if _, err := lane.Claim(context.Background(), "local-nope"); err == nil {
		t.Fatal("a claim on an unknown review answered")
	}
}

// A new approval interrupts nothing: no dialog opens, the header and the
// Approvals tab call for attention, and the person opens it with enter.
func TestANewApprovalDoesNotInterrupt(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	r := NewReviewer()
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	m.reviewer, m.operator = r, "alice"
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 36})

	if _, err := r.For("pg").File(context.Background(), "delete from users"); err != nil {
		t.Fatal(err)
	}
	tm, _ = tm.Update(localReviewsMsg(r.Snapshot()))
	mm := tm.(model)
	if mm.modal != "" {
		t.Fatalf("a new approval opened the dialog on its own: %q", mm.modal)
	}
	if mm.tab != tabWire {
		t.Fatalf("a new approval moved the screen to tab %d", mm.tab)
	}
	screen := ansi.Strip(mm.View())
	for _, want := range []string{"1 awaiting approval · press 3", "3 Approvals (1)"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen does not call for attention with %q", want)
		}
	}
	// The call for attention moves: two moments draw it differently. Color
	// is on for this check; a test process has no terminal to detect.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)
	a := shimmer("1 awaiting approval", now)
	b := shimmer("1 awaiting approval", now.Add(210*time.Millisecond))
	if a == b || ansi.Strip(a) != ansi.Strip(b) {
		t.Error("shimmer does not animate, or changes the text it animates")
	}
}

// On the Approvals tab, enter opens the newest waiting approval; focus starts
// on Reject; deciding closes the dialog and selects the next one waiting.
func TestApprovalDialogFlow(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	r := NewReviewer()
	r.now = func() time.Time { return now }
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	m.reviewer, m.operator = r, "alice"
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 36})

	first, _ := r.For("pg").File(context.Background(), "delete from users")
	second, _ := r.For("pg").File(context.Background(), "drop table audit")
	tm, _ = tm.Update(localReviewsMsg(r.Snapshot()))
	tm, _ = tm.Update(key("3"))
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := tm.(model)
	if mm.modal != second.ID {
		t.Fatalf("enter opened %q, want the newest approval %q", mm.modal, second.ID)
	}
	view := ansi.Strip(mm.View())
	for _, want := range []string{"Approval needed", "drop table audit", "Approve", "Reject", "1 more waiting"} {
		if !strings.Contains(view, want) {
			t.Errorf("dialog does not show %q", want)
		}
	}

	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := r.reviews[second.ID].Status; got != statusRejected {
		t.Fatalf("enter on the opened dialog = %s, want REJECTED: focus must start on Reject", got)
	}
	mm = tm.(model)
	if mm.modal != "" {
		t.Fatalf("the dialog chained to %q after an answer; it must close", mm.modal)
	}

	// The next one waiting is selected, one enter away.
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if mm = tm.(model); mm.modal != first.ID {
		t.Fatalf("enter after the answer opened %q, want the next waiting %q", mm.modal, first.ID)
	}
	tm, _ = tm.Update(key("a"))
	if got := r.reviews[first.ID]; got.Status != statusApproved || got.DecidedBy != "alice" {
		t.Fatalf("a = %+v, want APPROVED by alice", got)
	}
	mm = tm.(model)
	if rv := mm.st.Reviews[first.ID]; rv.Status != statusApproved || !rv.Local {
		t.Fatalf("the Approvals tab did not record the approval: %+v", rv)
	}
	if mm.st.PendingReviews() != 0 || strings.Contains(ansi.Strip(mm.View()), "awaiting approval · press") {
		t.Error("the screen still calls for attention with nothing waiting")
	}
}

// Esc closes the dialog undecided; enter brings it back.
func TestApprovalDialogEscCloses(t *testing.T) {
	now := time.Now()
	r := NewReviewer()
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	m.reviewer, m.operator = r, "alice"
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	res, _ := r.For("pg").File(context.Background(), "delete from t")
	tm, _ = tm.Update(localReviewsMsg(r.Snapshot()))
	tm, _ = tm.Update(key("3"))
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm := tm.(model)
	if mm.modal != "" || r.reviews[res.ID].Status != statusPending {
		t.Fatalf("esc answered or kept the dialog: modal=%q status=%s", mm.modal, r.reviews[res.ID].Status)
	}
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if mm = tm.(model); mm.modal != res.ID {
		t.Fatalf("enter on the waiting approval did not reopen it: %q", mm.modal)
	}
}

// The dialog fits every terminal the rest of the TUI fits, even with a
// statement far longer than the screen.
func TestApprovalDialogFits(t *testing.T) {
	now := time.Now()
	r := NewReviewer()
	long := strings.Repeat("update accounts set balance = 0 where id = 1;\n", 200)
	if _, err := r.For("pg").File(context.Background(), long); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{60, 12}, {80, 24}, {140, 40}} {
		m := newModel("dev", nil, func() time.Time { return now }, nil)
		m.reviewer, m.operator = r, "alice"
		var tm tea.Model = m
		tm, _ = tm.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		tm, _ = tm.Update(localReviewsMsg(r.Snapshot()))
		tm, _ = tm.Update(key("3"))
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if tm.(model).modal == "" {
			t.Fatalf("%dx%d: enter did not open the dialog", size[0], size[1])
		}
		lines := strings.Split(tm.(model).View(), "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d rows", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: row %d is %d wide", size[0], size[1], i, w)
				break
			}
		}
		if size[1] >= 24 && !strings.Contains(ansi.Strip(tm.(model).View()), "Approve") {
			t.Errorf("%dx%d: the buttons were pushed off the screen", size[0], size[1])
		}
	}
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
