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
	// localReviewsMsg is the terminal Reviewer's state after a change.
	localReviewsMsg []LocalReview
	// droppedMsg is how many captured lines the UI could not keep up with.
	droppedMsg int64
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

	// reviewer is the terminal's review backend, nil when the sidecar
	// files with a control plane or holds nothing. operator is who
	// decides here, recorded with each decision.
	reviewer *Reviewer
	operator string
	// modal is the local review the approval dialog shows, "" when it is
	// closed. approveFocused is which button enter presses; it starts on
	// Reject so a stray enter never releases a statement. skipped holds
	// the reviews put aside with esc, so the dialog does not reopen them.
	modal          string
	approveFocused bool
	skipped        map[string]bool

	// dropped counts captured lines thrown away because the screen fell
	// behind; the daemon is never made to wait for the screen.
	dropped int64
}

func newModel(version string, notes []string, now func() time.Time, stop func()) model {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "filter: lane, principal, table, rule, text…"
	ti.CharLimit = 200
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = stPrimary
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
		skipped: map[string]bool{},
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
	case localReviewsMsg:
		for _, lr := range msg {
			was := ""
			if prev := m.st.Reviews[lr.ID]; prev != nil && prev.Local {
				was = prev.Status
			}
			if was == lr.Status {
				continue
			}
			m.st.ApplyLocalReview(lr)
			switch lr.Status {
			case statusPending:
				if m.modal == "" && !m.skipped[lr.ID] {
					m.openModal(lr.ID)
				}
			case statusExecuted:
				m.st.ApplyLog(LogRecord{Time: m.now(), Level: "INFO", Msg: "approved statement released",
					Attrs: []Attr{{"review", lr.ID}, {"listener", lr.Listener}}})
			}
		}
		return m, nil
	case droppedMsg:
		m.dropped = int64(msg)
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
	if m.modal != "" {
		return m.modalKey(k)
	}
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
		if id := m.selectedLocalPending(); id != "" {
			m.openModal(id)
			return m, nil
		}
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

// modalKey handles keys while the approval dialog is open. a and r decide at
// once; enter presses the focused button; esc puts the review aside and
// shows the next one waiting.
func (m model) modalKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return m.quit()
	case "a", "y":
		m.decide(true)
	case "r", "n":
		m.decide(false)
	case "tab", "shift+tab", "left", "right", "h", "l":
		m.approveFocused = !m.approveFocused
	case "enter":
		m.decide(m.approveFocused)
	case "esc":
		m.skipped[m.modal] = true
		m.openModal(m.st.NextLocalPending(m.skipped))
	}
	return m, nil
}

func (m *model) openModal(id string) {
	m.modal = id
	m.approveFocused = false
	if id != "" {
		delete(m.skipped, id)
	}
}

// decide answers the review in the dialog and records who answered. The
// audit trail has no field for an approver, so the operational log carries
// it, and the summary printed at exit repeats it.
func (m *model) decide(approve bool) {
	if m.reviewer == nil || m.modal == "" {
		return
	}
	lr, ok := m.reviewer.Decide(m.modal, approve, m.operator)
	if ok {
		m.st.ApplyLocalReview(lr)
		msg := "review rejected"
		if approve {
			msg = "review approved"
		}
		m.st.ApplyLog(LogRecord{Time: m.now(), Level: "INFO", Msg: msg, Attrs: []Attr{
			{"review", lr.ID}, {"listener", lr.Listener}, {"by", lr.DecidedBy}}})
	}
	m.openModal(m.st.NextLocalPending(m.skipped))
}

// selectedLocalPending is the review under the cursor on the Reviews tab
// when it is local and still waiting, so enter can open it.
func (m model) selectedLocalPending() string {
	if m.tab != tabReviews || m.reviewer == nil {
		return ""
	}
	keys := m.keys(tabReviews)
	if len(keys) == 0 {
		return ""
	}
	id := keys[indexOf(keys, m.cur[tabReviews].sel, m.cur[tabReviews].follow)]
	if r := m.st.Reviews[id]; r != nil && r.Local && r.Status == statusPending {
		return id
	}
	return ""
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
