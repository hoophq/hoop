package sidecartui

import (
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

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
	// tabTour is the demo's guided section. Last, so a dashboard without the
	// demo hides it by counting one tab less (numTabs).
	tabTour
	tabCount
)

var tabNames = [tabCount]string{"Wire", "Sessions", "Approvals", "Listeners", "System", "Logs", "Try it"}

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

// cursor is one section's position. sel is the key of the selected row
// rather than its index, so a row stays selected while new ones arrive above
// it. An empty sel is the top row, whichever row is newest: a person at the
// top of a list watches traffic arrive, and one who moved down keeps the row
// they chose. off scrolls a section that has no rows (System).
type cursor struct {
	sel string
	off int
}

type model struct {
	st      *State
	now     func() time.Time
	version string
	notes   []string // facts the capture set up, shown on System

	width, height int
	tab           tab
	cur           [tabCount]cursor
	deniedOnly    bool
	// zoom shows the selected row's details over the whole content area,
	// opened with enter and closed with esc.
	zoom bool
	// menuFocus is true while the arrows move through the section menu,
	// false once the person went into a section. It starts on the menu:
	// the first thing anyone does is pick where to look.
	menuFocus bool
	// confirmQuit is the "stop the sidecar?" prompt q opens. quitYes is
	// its focused answer, No by default, so an enter after a stray q keeps
	// the sidecar running.
	confirmQuit bool
	quitYes     bool

	searching bool
	search    textinput.Model
	spin      spinner.Model

	// stop asks the daemon to shut down (SIGINT to this process, which the
	// daemon already handles). Stopping is drawn until doneMsg arrives.
	stop     func() error
	stopErr  error
	stopping bool
	done     bool
	err      error

	// reviewer is the terminal's review backend, nil when the sidecar
	// files with a control plane or holds nothing. operator is who
	// decides here, recorded with each decision.
	reviewer *Reviewer
	operator string
	// modal is the local approval the dialog shows, "" when it is closed.
	// It opens only when the person presses enter on the Approvals tab.
	// approveFocused is which button enter presses; it starts on Reject so
	// a stray enter never releases a statement.
	modal          string
	approveFocused bool
	// modalOff scrolls the statement inside the dialog. Approve stays off
	// until the last line has been on screen: a statement taller than the
	// dialog must not be released with its tail unread.
	modalOff int
	// record writes a decision to the daemon's log stream (runTUI sets
	// it), so it is saved with the log and not only drawn. Nil in tests,
	// where the decision goes straight into the Logs section.
	record func(msg string, args ...any)

	// dropped counts captured lines thrown away because the screen fell
	// behind; the daemon is never made to wait for the screen.
	dropped int64

	// tour is the demo's guided section, nil when the sidecar does not
	// front the demo API.
	tour *tour
}

// numTabs is how many sections the menu shows: Try it only with the demo.
func (m model) numTabs() tab {
	if m.tour == nil {
		return tabCount - 1
	}
	return tabCount
}

func newModel(version string, notes []string, now func() time.Time, stop func() error) model {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "filter: listener, user, table, rule, text…"
	ti.CharLimit = 200
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = stPrimary
	if version == "" || version == "unknown" {
		// A build without -ldflags, which is every local `make` build.
		version = "dev"
	}
	m := model{
		st:        NewState(now()),
		now:       now,
		version:   version,
		notes:     notes,
		search:    ti,
		spin:      sp,
		stop:      stop,
		menuFocus: true,
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
		if m.tour != nil {
			m.tour.see(audit.Event(msg))
		}
		return m, nil
	case tourResultMsg:
		if m.tour != nil {
			m.tour.apply(tourResult(msg))
		}
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
			// A new approval never opens anything: it would take the
			// screen from whatever the person was reading. The Approvals
			// tab and the header shimmer instead, and the person decides
			// when to go there.
			m.st.ApplyLocalReview(lr)
			if lr.Status == statusExecuted {
				m.st.ApplyLog(LogRecord{Time: m.now(), Level: "INFO", Msg: "approved statement released",
					Attrs: []Attr{{"approval", lr.ID}, {"listener", lr.Listener}}})
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
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.confirmQuit {
		return m.confirmKey(k)
	}
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

	if m.menuFocus {
		return m.menuKey(k)
	}

	if m.tab == tabTour && m.tour != nil {
		if cmd, used := m.tour.key(k); used {
			return m, cmd
		}
	}

	switch k.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "tab":
		m.tab, m.zoom = (m.tab+1)%m.numTabs(), false
	case "shift+tab":
		m.tab, m.zoom = (m.tab+m.numTabs()-1)%m.numTabs(), false
	case "1", "2", "3", "4", "5", "6", "7":
		if t := tab(k.String()[0] - '1'); t < m.numTabs() {
			m.tab, m.zoom = t, false
		}
	case "left", "h":
		m.zoom, m.menuFocus = false, true
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+u":
		m.move(-m.listHeight() / 2)
	case "pgdown", "ctrl+d":
		m.move(m.listHeight() / 2)
	case "home", "g":
		m.cur[m.tab].sel, m.cur[m.tab].off = "", 0
	case "end", "G":
		m.move(1 << 20)
	case "enter", "space":
		if id := m.selectedLocalPending(); id != "" {
			m.openModal(id)
			return m, nil
		}
		if m.tab != tabSystem && len(m.keys(m.tab)) > 0 {
			m.zoom = !m.zoom
		}
	case "d":
		if m.tab == tabWire {
			m.deniedOnly = !m.deniedOnly
			m.cur[tabWire].sel = ""
		}
	case "/":
		m.searching = true
		return m, tea.Batch(m.search.Focus(), textinput.Blink)
	case "esc":
		// One step back per press: out of the opened row, then off the
		// filters, then out to the menu.
		switch {
		case m.zoom:
			m.zoom = false
		case m.search.Value() != "" || m.deniedOnly:
			m.search.SetValue("")
			m.deniedOnly = false
		default:
			m.menuFocus = true
		}
	}
	return m, nil
}

// menuKey moves through the section menu. The content follows the
// highlighted section as the arrows move, so the menu doubles as a preview;
// enter or the right arrow goes into the section.
func (m model) menuKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "up", "k", "shift+tab":
		m.tab = (m.tab + m.numTabs() - 1) % m.numTabs()
	case "down", "j", "tab":
		m.tab = (m.tab + 1) % m.numTabs()
	case "1", "2", "3", "4", "5", "6", "7":
		if t := tab(k.String()[0] - '1'); t < m.numTabs() {
			m.tab, m.menuFocus = t, false
		}
	case "enter", "space", "right", "l":
		m.menuFocus = false
	case "/":
		m.menuFocus = false
		m.searching = true
		return m, tea.Batch(m.search.Focus(), textinput.Blink)
	}
	m.zoom = false
	return m, nil
}

// confirmKey answers the "stop the sidecar?" prompt. y and n answer at once;
// enter takes the focused answer, which starts on No; esc is No. A second
// ctrl+c is Yes, so a person who means it is never asked twice.
func (m model) confirmKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "y", "ctrl+c":
		return m.stopNow()
	case "n", "esc", "q":
		m.confirmQuit = false
	case "tab", "shift+tab", "left", "right", "h", "l":
		m.quitYes = !m.quitYes
	case "enter":
		if m.quitYes {
			return m.stopNow()
		}
		m.confirmQuit = false
	}
	return m, nil
}

// modalKey handles keys while the approval dialog is open. a and r decide at
// once; enter presses the focused button; esc closes it undecided; the arrows
// scroll a statement taller than the dialog. Approve does nothing until the
// whole statement has been shown (canApprove); Reject always works.
func (m model) modalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		return m.quit()
	case "a", "y":
		if m.canApprove() {
			m.decide(true)
		}
	case "r", "n":
		m.decide(false)
	case "tab", "shift+tab", "left", "right", "h", "l":
		m.approveFocused = !m.approveFocused
	case "enter":
		if !m.approveFocused || m.canApprove() {
			m.decide(m.approveFocused)
		}
	case "up", "k":
		m.scrollModal(-1)
	case "down", "j":
		m.scrollModal(1)
	case "pgup":
		m.scrollModal(-max(m.modalRoom()-1, 1))
	case "pgdown", "space":
		m.scrollModal(max(m.modalRoom()-1, 1))
	case "esc":
		m.modal = ""
	}
	return m, nil
}

func (m *model) openModal(id string) {
	m.modal = id
	m.approveFocused = false
	m.modalOff = 0
}

func (m *model) scrollModal(delta int) {
	total, room := len(m.modalStatement()), m.modalRoom()
	m.modalOff = min(max(m.modalOff+delta, 0), max(total-room, 0))
}

// canApprove is whether the last line of the held statement has been on
// screen: it fits the dialog, or the person scrolled to its end.
func (m model) canApprove() bool {
	total, room := len(m.modalStatement()), m.modalRoom()
	return m.modalOff+room >= total
}

// decide answers the approval in the dialog and records who answered. The
// audit trail has no field for an approver, so the operational log carries
// it, and the summary printed at exit repeats it.
//
// The dialog then closes, with the next approval still waiting selected on
// the tab: one enter away, never opened on its own.
func (m *model) decide(approve bool) {
	if m.reviewer == nil || m.modal == "" {
		return
	}
	lr, ok := m.reviewer.Decide(m.modal, approve, m.operator)
	if ok {
		m.st.ApplyLocalReview(lr)
		msg := "approval rejected"
		if approve {
			msg = "approval granted"
		}
		if m.record != nil {
			m.record(msg, "approval", lr.ID, "listener", lr.Listener, "by", lr.DecidedBy)
		} else {
			m.st.ApplyLog(LogRecord{Time: m.now(), Level: "INFO", Msg: msg, Attrs: []Attr{
				{"approval", lr.ID}, {"listener", lr.Listener}, {"by", lr.DecidedBy}}})
		}
	}
	m.modal = ""
	if next := m.st.NextLocalPending(nil); next != "" {
		m.cur[tabReviews].sel = next
	}
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
	id := keys[indexOf(keys, m.cur[tabReviews].sel)]
	if r := m.st.Reviews[id]; r != nil && r.Local && r.Status == statusPending {
		return id
	}
	return ""
}

// quit asks before stopping: q sits next to keys people type all day, and
// stopping the sidecar closes every listener. Once stopping, it leaves at
// once, for a daemon stuck in its shutdown.
func (m model) quit() (tea.Model, tea.Cmd) {
	if m.stopping || m.done || m.stop == nil {
		return m, tea.Quit
	}
	m.confirmQuit, m.quitYes = true, false
	return m, nil
}

// stopNow stops the daemon and waits for it to return.
func (m model) stopNow() (tea.Model, tea.Cmd) {
	m.confirmQuit = false
	if m.stopping || m.done || m.stop == nil {
		return m, tea.Quit
	}
	if err := m.stop(); err != nil {
		// The interrupt could not be sent, so the daemon is not stopping
		// and waiting would only hide that. Leave; Run reports it.
		m.stopErr = err
		return m, tea.Quit
	}
	m.stopping = true
	return m, nil
}

// move shifts the selection by delta rows. Back at the top it selects the
// top again rather than that row, so new rows arriving keep it on the newest.
func (m *model) move(delta int) {
	keys := m.keys(m.tab)
	c := &m.cur[m.tab]
	if len(keys) == 0 {
		c.off = max(c.off+delta, 0)
		return
	}
	i := min(max(indexOf(keys, c.sel)+delta, 0), len(keys)-1)
	c.sel = keys[i]
	if i == 0 {
		c.sel = ""
	}
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

// indexOf finds the selected key. The top (sel "") and a key that scrolled
// away select the newest row.
func indexOf(keys []string, sel string) int {
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
