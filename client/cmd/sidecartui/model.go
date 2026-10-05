package sidecartui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hoophq/hoop/sidecar/audit"
)

type tab int

const (
	tabWire tab = iota
	tabSessions
	tabReviews
	tabLanes
	tabSystem
	tabLogs
	tabCount
)

var tabNames = [tabCount]string{"Wire", "Sessions", "Reviews", "Lanes", "System", "Logs"}

// Messages the capture goroutines and the clock send in.
type (
	logMsg   LogRecord
	auditMsg audit.Event
	rawMsg   string
	tickMsg  time.Time
	// doneMsg says the daemon returned. err is what it returned.
	doneMsg struct{ err error }
)

// cursor is one tab's position. sel is the key of the selected row rather
// than its index, so a row stays selected while new ones arrive above it.
type cursor struct {
	sel    string
	off    int
	follow bool
}

type model struct {
	st      *State
	now     func() time.Time
	version string
	notes   []string // facts the capture set up, shown on System

	width, height int
	tab           tab
	cur           [tabCount]cursor
	detail        bool
	deniedOnly    bool

	searching bool
	search    textinput.Model
	spin      spinner.Model

	// stop asks the daemon to shut down (SIGINT to this process, which the
	// daemon already handles). Stopping is drawn until doneMsg arrives.
	stop     func()
	stopping bool
	done     bool
	err      error
}

func newModel(version string, notes []string, now func() time.Time, stop func()) model {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "filter: lane, principal, table, rule, text…"
	ti.CharLimit = 200
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = stAccent
	if version == "" || version == "unknown" {
		// A build without -ldflags, which is every local `make` build.
		version = "dev"
	}
	m := model{
		st:      NewState(now()),
		now:     now,
		version: version,
		notes:   notes,
		detail:  true,
		search:  ti,
		spin:    sp,
		stop:    stop,
	}
	for i := range m.cur {
		m.cur[i].follow = true
	}
	return m
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, tick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case logMsg:
		m.st.ApplyLog(LogRecord(msg))
		return m, nil
	case auditMsg:
		m.st.ApplyAudit(audit.Event(msg))
		return m, nil
	case rawMsg:
		// A line that is not JSON: a panic, a library's own print. Kept
		// as a log so it is not lost behind the screen.
		m.st.ApplyLog(LogRecord{Time: m.now(), Level: "RAW", Msg: string(msg)})
		return m, nil
	case tickMsg:
		return m, tick()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case doneMsg:
		m.done, m.err = true, msg.err
		return m, tea.Quit
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		switch k.String() {
		case "enter":
			m.searching = false
			m.search.Blur()
			return m, nil
		case "esc":
			m.searching = false
			m.search.Blur()
			m.search.SetValue("")
			return m, nil
		case "ctrl+c":
			return m.quit()
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(k)
		return m, cmd
	}

	switch k.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "tab", "right", "l":
		m.tab = (m.tab + 1) % tabCount
	case "shift+tab", "left", "h":
		m.tab = (m.tab + tabCount - 1) % tabCount
	case "1", "2", "3", "4", "5", "6":
		m.tab = tab(k.String()[0] - '1')
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+u":
		m.move(-m.listHeight() / 2)
	case "pgdown", "ctrl+d":
		m.move(m.listHeight() / 2)
	case "home", "g":
		m.cur[m.tab].follow = true
	case "end", "G":
		m.move(1 << 20)
	case "f":
		m.cur[m.tab].follow = !m.cur[m.tab].follow
	case "enter", " ":
		m.detail = !m.detail
	case "d":
		m.deniedOnly = !m.deniedOnly
		m.cur[tabWire].follow = true
	case "/":
		m.searching = true
		m.search.Focus()
		return m, textinput.Blink
	case "esc":
		m.search.SetValue("")
		m.deniedOnly = false
	}
	return m, nil
}

// quit stops the daemon and waits for it; a second press leaves at once, for
// a daemon stuck in its shutdown.
func (m model) quit() (tea.Model, tea.Cmd) {
	if m.stopping || m.done || m.stop == nil {
		return m, tea.Quit
	}
	m.stopping = true
	m.stop()
	return m, nil
}

// move shifts the selection by delta rows and stops following.
func (m *model) move(delta int) {
	keys := m.keys(m.tab)
	c := &m.cur[m.tab]
	if len(keys) == 0 {
		c.off = max(c.off+delta, 0)
		return
	}
	i := indexOf(keys, c.sel, c.follow)
	i = min(max(i+delta, 0), len(keys)-1)
	c.sel = keys[i]
	c.follow = i == 0
}

// keys returns the row keys of a tab, in display order, under the current
// filters. Index 0 is the newest row.
func (m model) keys(t tab) []string {
	var out []string
	switch t {
	case tabWire:
		for _, i := range m.wireRows() {
			out = append(out, strconv.Itoa(m.st.FeedDropped+i))
		}
	case tabSessions:
		for _, s := range m.sessionRows() {
			out = append(out, s.ID)
		}
	case tabReviews:
		for _, r := range m.reviewRows() {
			out = append(out, r.ID)
		}
	case tabLanes:
		out = append(out, m.st.LaneOrder...)
	case tabLogs:
		for _, i := range m.logRows() {
			out = append(out, strconv.Itoa(m.st.LogsDropped+i))
		}
	}
	return out
}

// indexOf finds the selected key. Following, or a key that scrolled away,
// selects the newest row.
func indexOf(keys []string, sel string, follow bool) int {
	if follow {
		return 0
	}
	for i, k := range keys {
		if k == sel {
			return i
		}
	}
	return 0
}

func (m model) query() string { return strings.ToLower(strings.TrimSpace(m.search.Value())) }

func matches(q string, fields ...string) bool {
	if q == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// wireRows returns indexes into Feed, newest first.
func (m model) wireRows() []int {
	q := m.query()
	var out []int
	for i := len(m.st.Feed) - 1; i >= 0; i-- {
		ev := m.st.Feed[i]
		if m.deniedOnly && ev.Kind != audit.KindViolation && ev.Kind != audit.KindError {
			continue
		}
		if !matches(q, ev.Connection, ev.Principal, ev.Statement, ev.Rule, ev.Message,
			string(ev.Operation), string(ev.Kind), string(ev.SessionID), strings.Join(ev.Tables, " "),
			ev.Metadata[metaReviewID], ev.Metadata[metaActivity]) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func (m model) sessionRows() []*Session {
	q := m.query()
	var out []*Session
	for _, s := range m.st.SessionList() {
		if matches(q, s.ID, s.Lane, s.Principal, s.Protocol, s.Last) {
			out = append(out, s)
		}
	}
	return out
}

func (m model) reviewRows() []*Review {
	q := m.query()
	var out []*Review
	for i := len(m.st.ReviewOrder) - 1; i >= 0; i-- {
		r := m.st.Reviews[m.st.ReviewOrder[i]]
		if r != nil && matches(q, r.ID, r.Status, r.Lane, r.Principal, r.Statement, r.Message) {
			out = append(out, r)
		}
	}
	return out
}

func (m model) logRows() []int {
	q := m.query()
	var out []int
	for i := len(m.st.Logs) - 1; i >= 0; i-- {
		r := m.st.Logs[i]
		fields := []string{r.Level, r.Msg}
		for _, a := range r.Attrs {
			fields = append(fields, a.Value)
		}
		if matches(q, fields...) {
			out = append(out, i)
		}
	}
	return out
}
