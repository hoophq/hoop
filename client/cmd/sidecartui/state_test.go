package sidecartui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/sidecar/audit"
)

// Lines as the daemon writes them: slog JSON on stderr, audit JSON on stdout.
var script = []string{
	`{"time":"2026-10-05T12:00:00Z","level":"INFO","msg":"license: missing, running the free tier."}`,
	`{"time":"2026-10-05T12:00:00Z","level":"INFO","msg":"rule limits","guardrail_rules":"1","mask_rules":"1"}`,
	`{"time":"2026-10-05T12:00:00Z","level":"INFO","msg":"risk analyzer attached","provider":"anthropic","model":"claude-haiku-4-5","send":"text","fail_open":false}`,
	`{"time":"2026-10-05T12:00:00Z","level":"INFO","msg":"lane ready","listener":"pg-prod","protocol":"postgres","upstream":"db:5432","enforcing":true,"observing":false,"rules":2,"opa":"","masking":true}`,
	`{"time":"2026-10-05T12:00:00Z","level":"INFO","msg":"hoop-inspect listening","network":"tcp","listen":"127.0.0.1:15432","upstream":"db:5432","protocol":"postgres","connection":"pg-prod"}`,
	`{"time":"2026-10-05T12:00:00Z","level":"WARN","msg":"upstream certificate verification is DISABLED","listener":"pg-prod"}`,
	`{"time":"2026-10-05T12:00:00Z","level":"INFO","msg":"admin endpoint listening","listen":"127.0.0.1:19000"}`,
	`{"kind":"session_start","timestamp":"2026-10-05T12:00:01Z","session_id":"s1","principal":"alice","protocol":"postgres","connection":"pg-prod","allowed":false}`,
	`{"time":"2026-10-05T12:00:01Z","level":"INFO","msg":"session opened","listener":"pg-prod","session":"s1","principal":"alice","metadata":{"app":"psql"},"upstream":"db:5432"}`,
	`{"kind":"statement","timestamp":"2026-10-05T12:00:02Z","session_id":"s1","principal":"alice","protocol":"postgres","connection":"pg-prod","operation":"select","statement":"select * from users","tables":["users"],"allowed":true,"metadata":{"risk_level":"low","ai_status":"classified"}}`,
	`{"kind":"masked","timestamp":"2026-10-05T12:00:02Z","session_id":"s1","principal":"alice","connection":"pg-prod","allowed":false,"masked_entities":["EMAIL_ADDRESS"],"masked_count":3}`,
	`{"kind":"violation","timestamp":"2026-10-05T12:00:03Z","session_id":"s1","principal":"alice","protocol":"postgres","connection":"pg-prod","operation":"delete","statement":"delete from users","allowed":false,"rule":"no-delete","message":"deletes need review: waiting for approval (review rv_1)","metadata":{"review_id":"rv_1","review_mode":"return","risk_level":"high"}}`,
	`{"kind":"statement","timestamp":"2026-10-05T12:00:09Z","session_id":"s1","principal":"alice","protocol":"postgres","connection":"pg-prod","operation":"delete","statement":"delete from users","allowed":true,"metadata":{"review_id":"rv_1"}}`,
	`{"kind":"session_end","timestamp":"2026-10-05T12:00:10Z","session_id":"s1","principal":"alice","connection":"pg-prod","allowed":false,"duration_ns":9000000000,"statement_count":3,"denied_count":1}`,
	`{"time":"2026-10-05T12:00:10Z","level":"INFO","msg":"session closed","listener":"pg-prod","session":"s1","principal":"alice","statements":3,"denied":1,"duration":"9s"}`,
	`{"kind":"session_start","timestamp":"2026-10-05T12:00:11Z","session_id":"s2","principal":"bob","protocol":"postgres","connection":"pg-prod","allowed":false}`,
	`panic: something printed raw`,
}

func feed(s *State, lines []string) (raw int) {
	for _, l := range lines {
		if ev, ok := ParseAudit([]byte(l)); ok {
			s.ApplyAudit(ev)
		} else if rec, ok := ParseLog([]byte(l)); ok {
			s.ApplyLog(rec)
		} else {
			raw++
		}
	}
	return raw
}

func TestStateFromScript(t *testing.T) {
	s := NewState(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
	if raw := feed(s, script); raw != 1 {
		t.Fatalf("raw lines = %d, want 1 (the panic line)", raw)
	}

	if s.Statements != 3 || s.Denied != 1 || s.Masked != 3 {
		t.Fatalf("totals = %d statements, %d denied, %d masked; want 3, 1, 3", s.Statements, s.Denied, s.Masked)
	}
	l := s.Lanes["pg-prod"]
	if l == nil || !l.Ready || !l.Enforcing || l.Listen != "127.0.0.1:15432" || l.Rules != "2" || !l.Masking {
		t.Fatalf("lane = %+v", l)
	}
	if l.Sessions != 2 {
		t.Fatalf("lane sessions = %d, want 2: the audit and the log both report s1 and it counts once", l.Sessions)
	}
	if len(l.Notes) != 1 || !strings.Contains(l.Notes[0], "DISABLED") {
		t.Fatalf("lane notes = %v", l.Notes)
	}

	open := s.OpenSessions()
	if len(open) != 1 || open[0].ID != "s2" {
		t.Fatalf("open sessions = %v, want only s2", open)
	}
	s1 := s.Sessions["s1"]
	if s1.Open || s1.Statements != 3 || s1.Denied != 1 || s1.Masked != 3 {
		t.Fatalf("s1 = %+v", s1)
	}

	r := s.Reviews["rv_1"]
	if r == nil || r.Status != "APPROVED" || r.Hits != 2 || r.Mode != "" {
		t.Fatalf("review = %+v, want APPROVED after the release with 2 hits", r)
	}
	if s.Risk["high"] != 1 || s.Risk["low"] != 1 || s.AIStatus["classified"] != 1 {
		t.Fatalf("risk = %v, ai = %v", s.Risk, s.AIStatus)
	}
	if s.System.Admin != "127.0.0.1:19000" || !strings.HasPrefix(s.System.License, "missing") ||
		s.System.Limits != "guardrails 1 · masks 1" || !strings.Contains(s.System.Analyzer, "anthropic") {
		t.Fatalf("system = %+v", s.System)
	}
	if len(s.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(s.Warnings))
	}
}

func TestReviewStatus(t *testing.T) {
	s := NewState(time.Now())
	feed(s, script[:12])
	if got := s.Reviews["rv_1"].Status; got != "PENDING" {
		t.Fatalf("status before release = %q, want PENDING", got)
	}
	if s.PendingReviews() != 1 {
		t.Fatalf("pending = %d, want 1", s.PendingReviews())
	}
}

func TestSparkCountsTheLastMinute(t *testing.T) {
	s := NewState(time.Now())
	now := time.Unix(1_000_000, 0)
	feed(s, []string{
		fmt.Sprintf(`{"kind":"statement","timestamp":%q,"session_id":"a","allowed":true}`, now.Add(-90*time.Second).Format(time.RFC3339)),
		fmt.Sprintf(`{"kind":"statement","timestamp":%q,"session_id":"a","allowed":true}`, now.Add(-2*time.Second).Format(time.RFC3339)),
		fmt.Sprintf(`{"kind":"violation","timestamp":%q,"session_id":"a","allowed":false}`, now.Format(time.RFC3339)),
	})
	stmts, denied := s.Spark(now)
	total := 0
	for _, v := range stmts {
		total += v
	}
	if total != 2 || stmts[sparkSeconds-1] != 1 || denied[sparkSeconds-1] != 1 || stmts[sparkSeconds-3] != 1 {
		t.Fatalf("spark = %v / %v; the 90s-old statement must not count", stmts, denied)
	}
}

func TestBuffersStayBounded(t *testing.T) {
	s := NewState(time.Now())
	for i := range maxFeed + 50 {
		s.ApplyAudit(mustAudit(t, fmt.Sprintf(`{"kind":"statement","timestamp":"2026-10-05T12:00:00Z","session_id":"x","statement":"select %d","allowed":true}`, i)))
	}
	if len(s.Feed) != maxFeed || s.FeedDropped != 50 {
		t.Fatalf("feed = %d, dropped = %d", len(s.Feed), s.FeedDropped)
	}
	if got := s.Feed[0].Statement; got != "select 50" {
		t.Fatalf("oldest kept = %q, want select 50", got)
	}
	for i := range maxClosed + 10 {
		id := fmt.Sprintf("s%d", i)
		s.ApplyAudit(mustAudit(t, `{"kind":"session_start","timestamp":"2026-10-05T12:00:00Z","session_id":"`+id+`"}`))
		s.ApplyAudit(mustAudit(t, `{"kind":"session_end","timestamp":"2026-10-05T12:00:01Z","session_id":"`+id+`"}`))
	}
	// The closed ones are bounded; "x", whose statements opened it above
	// and which never ended, is open and stays.
	if len(s.Sessions) != maxClosed+1 || !s.Sessions["x"].Open {
		t.Fatalf("sessions kept = %d, want %d closed plus the open x", len(s.Sessions), maxClosed)
	}
}

func mustAudit(t *testing.T, line string) (ev audit.Event) {
	t.Helper()
	ev, ok := ParseAudit([]byte(line))
	if !ok {
		t.Fatalf("not an audit line: %s", line)
	}
	return ev
}

func TestTextLine(t *testing.T) {
	rec, ok := ParseLog([]byte(`{"time":"2026-10-05T12:00:01Z","level":"INFO","msg":"session opened","listener":"pg-prod","metadata":{"app":"psql"},"n":3,"ok":true}`))
	if !ok {
		t.Fatal("ParseLog refused a slog line")
	}
	got := TextLine(rec)
	want := `time=2026-10-05T12:00:01Z level=INFO msg="session opened" listener=pg-prod metadata.app=psql n=3 ok=true`
	if got != want {
		t.Fatalf("TextLine =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "\x1b") {
		t.Fatal("text output carries an ANSI escape")
	}
	if _, ok := ParseLog([]byte(`{"kind":"statement","session_id":"s"}`)); ok {
		t.Fatal("ParseLog took an audit line for a log record")
	}
}

// TestViewRenders drives the model through every tab at the sizes a
// terminal realistically has, and checks no frame is wider or taller than
// the terminal: an overflowing frame scrolls the alt screen and tears.
func TestViewRenders(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 20, 0, time.UTC)
	m := newModel("1.2.3", []string{"audit trail captured from stdout"}, func() time.Time { return now }, nil)
	for _, l := range script {
		if ev, ok := ParseAudit([]byte(l)); ok {
			m.st.ApplyAudit(ev)
		} else if rec, ok := ParseLog([]byte(l)); ok {
			m.st.ApplyLog(rec)
		}
	}
	m.tour = newTour(&DemoOptions{})
	for _, size := range [][2]int{{60, 14}, {80, 24}, {109, 30}, {140, 40}, {220, 60}} {
		var tm tea.Model = m
		tm, _ = tm.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for tb := range tabCount {
			for _, mode := range []string{"list", "open", "quit"} {
				mm := tm.(model)
				mm.tab = tb
				mm.move(1)
				switch mode {
				case "open":
					mm.zoom = true
				case "quit":
					mm.confirmQuit = true
				}
				out := mm.render()
				lines := strings.Split(out, "\n")
				if len(lines) != size[1] {
					t.Errorf("%dx%d %s %s: %d rows", size[0], size[1], tabNames[tb], mode, len(lines))
				}
				for i, line := range lines {
					if w := ansi.StringWidth(line); w > size[0] {
						t.Errorf("%dx%d %s %s: row %d is %d wide", size[0], size[1], tabNames[tb], mode, i, w)
						break
					}
				}
			}
		}
	}
}

// q asks first, and the answer an enter gives is No: a stray q must not stop
// the sidecar. y, or enter after moving to Yes, stops it.
func TestQuitAsksAndDefaultsToNo(t *testing.T) {
	stopped := 0
	m := newModel("dev", nil, time.Now, func() error { stopped++; return nil })
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	tm, _ = tm.Update(key("q"))
	if mm := tm.(model); !mm.confirmQuit || stopped != 0 {
		t.Fatalf("q did not ask first: confirm=%v stopped=%d", mm.confirmQuit, stopped)
	}
	if !strings.Contains(ansi.Strip(tm.(model).render()), "Stop the sidecar?") {
		t.Fatal("the prompt is not on screen")
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if mm := tm.(model); mm.confirmQuit || stopped != 0 || mm.stopping {
		t.Fatalf("enter on the prompt stopped the sidecar: stopped=%d", stopped)
	}

	tm, _ = tm.Update(key("q"))
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if stopped != 0 || tm.(model).confirmQuit {
		t.Fatal("esc on the prompt stopped the sidecar or kept the prompt")
	}

	tm, _ = tm.Update(key("q"))
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if mm := tm.(model); stopped != 1 || !mm.stopping {
		t.Fatalf("Yes did not stop the sidecar: stopped=%d", stopped)
	}
}

// The arrows start in the section menu: up and down pick a section and the
// content follows; enter or the right arrow goes in; there the arrows move
// rows, enter opens one, and esc or the left arrow steps back out. The
// hints row names each step before anyone presses it.
func TestArrowsMoveThroughTheMenuThenTheSection(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 20, 0, time.UTC)
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	feed(m.st, script)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 140, Height: 36})
	press := func(k rune) { tm, _ = tm.Update(tea.KeyPressMsg{Code: k}) }
	screen := func() string { return ansi.Strip(tm.(model).render()) }

	if !tm.(model).menuFocus || !strings.Contains(screen(), "↑↓ choose a section") {
		t.Fatal("the arrows do not start in the menu, or the hints do not say so")
	}
	press(tea.KeyDown)
	press(tea.KeyDown)
	if got := tm.(model).tab; got != tabReviews {
		t.Fatalf("two downs in the menu reached section %d, want Approvals", got)
	}
	press(tea.KeyUp)
	if got := tm.(model).tab; got != tabSessions {
		t.Fatalf("up in the menu reached section %d, want Sessions", got)
	}
	press(tea.KeyUp)
	press(tea.KeyUp)
	if got := tm.(model).tab; got != tabLogs {
		t.Fatalf("up past the first section reached %d, want it to wrap to Logs", got)
	}
	press(tea.KeyDown) // back to Wire

	press(tea.KeyRight)
	if mm := tm.(model); mm.menuFocus || !strings.Contains(screen(), "enter open") {
		t.Fatal("right did not go into the section, or the hints do not say enter opens")
	}
	press(tea.KeyDown)
	if tm.(model).cur[tabWire].sel == "" {
		t.Fatal("down inside the section did not move the row")
	}
	press(tea.KeyEnter)
	if mm := tm.(model); !mm.zoom || !strings.Contains(screen(), "esc back to the list") {
		t.Fatal("enter did not open the selected row")
	}
	press(tea.KeyEscape)
	if mm := tm.(model); mm.zoom || mm.menuFocus {
		t.Fatal("esc from the opened row did not go back to the list")
	}
	press(tea.KeyEscape)
	if !tm.(model).menuFocus {
		t.Fatal("esc from the list did not go back to the menu")
	}
	press(tea.KeyEnter)
	press(tea.KeyLeft)
	if !tm.(model).menuFocus {
		t.Fatal("left did not go back to the menu")
	}
	if strings.Contains(screen(), "follow") {
		t.Error("the screen still mentions follow")
	}
}

// The selected row carries the soft background across its whole width, even
// past the colored segments inside it, whose resets would otherwise clear it.
func TestSelectionBackgroundSurvivesColors(t *testing.T) {
	row := stDanger.Render("DENY") + " " + stStrong.Render("users")
	got := withBackground(row, 20)
	bg := selectionSequence()
	if bg == "" {
		t.Fatal("no selection background under a true-color profile")
	}
	if strings.Count(got, bg) < 3 {
		t.Fatalf("the background is not re-applied after each reset: %q", got)
	}
	if ansi.StringWidth(got) != 20 {
		t.Fatalf("the selected row is %d wide, want the full 20", ansi.StringWidth(got))
	}
}
