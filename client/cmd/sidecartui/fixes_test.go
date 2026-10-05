package sidecartui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

type line struct {
	text      string
	truncated bool
}

// lines collects what a reader emits; the follower emits from its own
// goroutine, so every read goes through the lock.
type lines struct {
	mu  sync.Mutex
	got []line
}

func (l *lines) emit(b []byte, truncated bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, line{string(b), truncated})
}

func (l *lines) all() []line {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]line(nil), l.got...)
}

func collect() (*lines, func([]byte, bool)) {
	l := &lines{}
	return l, l.emit
}

// A line past the limit is cut for the screen and the rest drained: the next
// line still arrives, so the daemon's pipe never stays full.
func TestReadLinesDrainsPastALongLine(t *testing.T) {
	long := strings.Repeat("x", 300*1024)
	got, emit := collect()
	err := readLines(strings.NewReader("first\n"+long+"\nlast\n"), 100*1024, emit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.all()) != 3 || got.all()[0].text != "first" || got.all()[2].text != "last" {
		t.Fatalf("lines = %d, want first, the long one, last", len(got.all()))
	}
	if mid := got.all()[1]; !mid.truncated || len(mid.text) != 100*1024 {
		t.Fatalf("the long line: truncated=%v len=%d", mid.truncated, len(mid.text))
	}
	if got.all()[0].truncated || got.all()[2].truncated {
		t.Fatal("a short line was marked truncated")
	}
}

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n == 0 {
		f.n++
		return copy(p, "partial line"), nil
	}
	return 0, errors.New("disk on fire")
}

// A read error ends the reader and is returned, after the partial line.
func TestReadLinesReturnsTheReadError(t *testing.T) {
	got, emit := collect()
	err := readLines(&failingReader{}, 1024, emit)
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("err = %v, want the read error", err)
	}
	if len(got.all()) != 1 || got.all()[0].text != "partial line" {
		t.Fatalf("the partial line was lost: %+v", got.all())
	}
}

// The follower reads what is appended, waits on a partial line, resumes from
// the start after a copy-and-truncate rotation, and caps an unterminated
// record instead of growing without bound.
func TestFollowAppendsRotationsAndLongRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	w, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.WriteString(strings.Repeat("old history\n", 50)); err != nil {
		t.Fatal(err)
	}
	r, _ := os.Open(path)
	if _, err := r.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	got, emit := collect()
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- follow(r, 64, emit, stop) }()

	wait := func(n int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if len(got.all()) >= n {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("lines = %+v, want %d", got.all(), n)
	}

	_, _ = w.WriteString("one\ntw")
	wait(1)
	_, _ = w.WriteString("o\n")
	wait(2)
	if got.all()[1].text != "two" {
		t.Fatalf("a line written in two parts came out as %q", got.all()[1].text)
	}

	// copy-and-truncate: the daemon's append-mode writes start over.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Seek(0, io.SeekStart)
	_, _ = w.WriteString("after rotation\n")
	wait(3)
	if got.all()[2].text != "after rotation" {
		t.Fatalf("after a truncation the follower read %q", got.all()[2].text)
	}

	_, _ = w.WriteString(strings.Repeat("y", 500) + "\nnext\n")
	wait(5)
	if l := got.all()[3]; !l.truncated || len(l.text) != 64 {
		t.Fatalf("an oversized record: truncated=%v len=%d", l.truncated, len(l.text))
	}
	if got.all()[4].text != "next" {
		t.Fatalf("the record after an oversized one is %q", got.all()[4].text)
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatalf("follow ended with %v", err)
	}
}

// A client chooses its statement's bytes and the approval dialog shows them:
// an ESC or a bidi override must reach the screen as a visible mark, never as
// a sequence the terminal runs.
func TestUntrustedTextCannotDriveTheTerminal(t *testing.T) {
	for in, want := range map[string]string{
		"DELETE FROM t\x1b[2K\x1b[1A SELECT 1": "DELETE FROM t␛[2K␛[1A SELECT 1",
		"a\rb":                                 `a\x0db`,
		"ok\x07":                               `ok\x07`,
		"x\u202ey":                             "x\\u202ey",
		"bad\xffbyte":                          `bad\xffbyte`,
		"multi\nline\twith tab":                "multi\nline\twith tab",
		"naïve – plain":                        "naïve – plain",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}

	now := time.Now()
	r := NewReviewer()
	if _, err := r.file("pg", "DELETE FROM users\x1b[2K\x1b[1A", analyzer.HoldDetail{
		RiskLevel: analyzer.RiskHigh, Title: "t\x1b]0;owned\x07", Explanation: "why\x1b[31m",
	}); err != nil {
		t.Fatal(err)
	}
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	m.reviewer, m.operator = r, "alice"
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 36})
	tm, _ = tm.Update(localReviewsMsg(r.Snapshot()))
	tm, _ = tm.Update(key("3"))
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	frame := tm.(model).render()
	for _, raw := range []string{"\x1b[2K", "\x1b[1A", "\x1b]0;owned", "\x07", "\x1b[31mwhy"} {
		if strings.Contains(frame, raw) {
			t.Errorf("the dialog passed %q to the terminal", raw)
		}
	}
	if !strings.Contains(ansi.Strip(frame), "DELETE FROM users␛[2K␛[1A") {
		t.Error("the dialog does not show the escape as a visible mark")
	}
}

// A statement taller than the dialog cannot be approved until its last line
// has been on screen. Reject works at once.
func TestApproveWaitsForTheWholeStatement(t *testing.T) {
	now := time.Now()
	r := NewReviewer()
	stmt := strings.Repeat("update accounts set note = 'fine' where id = 1;\n", 60) + "DROP TABLE accounts;"
	res, err := r.file("pg", stmt, analyzer.HoldDetail{})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	m.reviewer, m.operator = r, "alice"
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	tm, _ = tm.Update(localReviewsMsg(r.Snapshot()))
	tm, _ = tm.Update(key("3"))
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if strings.Contains(ansi.Strip(tm.(model).render()), "DROP TABLE") {
		t.Fatal("the test needs a statement taller than the dialog")
	}
	if !strings.Contains(ansi.Strip(tm.(model).render()), "scroll to the end to approve") {
		t.Error("the dialog does not say the statement must be read first")
	}
	tm, _ = tm.Update(key("a"))
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := r.reviews[res.ID].Status; got != statusPending {
		t.Fatalf("approved with the tail unread: %s", got)
	}

	for i := 0; i < 40; i++ {
		tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if !strings.Contains(ansi.Strip(tm.(model).render()), "DROP TABLE accounts;") {
		t.Fatal("scrolling did not reach the last line")
	}
	tm, _ = tm.Update(key("a"))
	if got := r.reviews[res.ID].Status; got != statusApproved {
		t.Fatalf("after reading it all, a = %s, want APPROVED", got)
	}
}

// A review nobody decided expires after the hold's wait, and so does an
// approval nobody used: neither stays a standing release for a later client.
func TestLocalApprovalsExpire(t *testing.T) {
	defer func(d time.Duration) { reviewLife = d }(reviewLife)
	reviewLife = time.Minute
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	r := NewReviewer()
	r.now = func() time.Time { return now }
	lane := r.For("pg")

	pending, _ := lane.File(context.Background(), "delete from a")
	approved, _ := lane.File(context.Background(), "delete from b")
	r.Decide(approved.ID, true, "alice")

	now = now.Add(2 * time.Minute)
	r.expire()
	for _, id := range []string{pending.ID, approved.ID} {
		if got := r.reviews[id].Status; got != statusExpired {
			t.Errorf("%s = %s, want EXPIRED", id, got)
		}
	}
	if _, ok := r.Decide(pending.ID, true, "alice"); ok {
		t.Error("an expired review was approved")
	}
	if c, _ := lane.Claim(context.Background(), approved.ID); c.Forward {
		t.Error("an expired approval released a statement")
	}
	again, _ := lane.File(context.Background(), "delete from b")
	if again.ID == approved.ID || again.Forward {
		t.Errorf("a resend after expiry reused the stale approval: %+v", again)
	}
}

// The terminal takes a bounded number of waiting reviews and refuses a
// statement past the control plane's size limit; both deny the hold.
func TestLocalApprovalsAreBounded(t *testing.T) {
	r := NewReviewer()
	lane := r.For("pg")
	if _, err := lane.File(context.Background(), strings.Repeat("x", maxStatementBytes+1)); err == nil {
		t.Error("a statement past the size limit was filed")
	}
	for i := 0; i < maxPending; i++ {
		if _, err := lane.File(context.Background(), "delete from t"+strings.Repeat("x", i)); err != nil {
			t.Fatalf("filing %d: %v", i, err)
		}
	}
	if _, err := lane.File(context.Background(), "one too many"); err == nil {
		t.Error("a review past the pending limit was filed")
	}
}

// A session whose start the screen never saw appears with its first
// statement, counted once when it later ends.
func TestASessionAppearsFromItsStatements(t *testing.T) {
	s := NewState(time.Now())
	feed(s, []string{
		`{"kind":"statement","timestamp":"2026-10-05T12:00:02Z","session_id":"late","principal":"carol","connection":"pg-prod","statement":"select 1","allowed":true}`,
	})
	sess := s.Sessions["late"]
	if sess == nil || !sess.Open || sess.Principal != "carol" || sess.Statements != 1 {
		t.Fatalf("session = %+v, want an open session for carol", sess)
	}
	feed(s, []string{
		`{"kind":"session_end","timestamp":"2026-10-05T12:00:09Z","session_id":"late","connection":"pg-prod","statement_count":1}`,
	})
	if s.Lanes["pg-prod"].Sessions != 1 {
		t.Fatalf("lane sessions = %d, want the late session counted once", s.Lanes["pg-prod"].Sessions)
	}
}

// A warning the daemon repeats is kept once on its listener, and the list is
// bounded.
func TestListenerNotesAreBounded(t *testing.T) {
	s := NewState(time.Now())
	feed(s, script)
	for i := 0; i < 100; i++ {
		feed(s, []string{`{"time":"2026-10-05T12:00:00Z","level":"WARN","msg":"analyzer rate limit reached","listener":"pg-prod"}`})
	}
	for i := 0; i < 30; i++ {
		feed(s, []string{`{"time":"2026-10-05T12:00:00Z","level":"WARN","msg":"warning ` + strings.Repeat("!", i) + `","listener":"pg-prod"}`})
	}
	notes := s.Lanes["pg-prod"].Notes
	if len(notes) != maxNotes {
		t.Fatalf("notes = %d, want %d", len(notes), maxNotes)
	}
	seen := map[string]bool{}
	for _, n := range notes {
		if seen[n] {
			t.Fatalf("note %q kept twice", n)
		}
		seen[n] = true
	}
}

// A statement cut for the screen never ends in half a character.
func TestLongStatementsCutOnACharacter(t *testing.T) {
	s := NewState(time.Now())
	stmt := strings.Repeat("a", maxStatementLen-1) + "é and more"
	ev := mustAudit(t, `{"kind":"statement","timestamp":"2026-10-05T12:00:00Z","session_id":"x","allowed":true}`)
	ev.Statement = stmt
	s.ApplyAudit(ev)
	if got := s.Feed[0].Statement; !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("the cut statement is not valid UTF-8 or lost its marker: %q", got[len(got)-8:])
	}
}

// When the interrupt cannot be sent, the TUI leaves and says so instead of
// waiting on a daemon that is not stopping.
func TestAStopThatFailsIsReported(t *testing.T) {
	m := newModel("dev", nil, time.Now, func() error { return errors.New("no signals here") })
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	tm, _ = tm.Update(key("q"))
	tm, cmd := tm.Update(key("y"))
	mm := tm.(model)
	if mm.stopErr == nil || mm.stopping {
		t.Fatalf("stopErr=%v stopping=%v, want the error and no wait", mm.stopErr, mm.stopping)
	}
	if cmd == nil {
		t.Fatal("the TUI did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("the TUI did not quit")
	}
}
