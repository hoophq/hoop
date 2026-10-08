package sidecartui

import (
	"fmt"
	"image/color"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/sidecar/audit"
)

// chrome is the rows above the body: the two header rows and the hints row.
const chrome = 3

// View lays the screen out as the header and the key hints across the top,
// then the section menu on the left and the section's content on the right.
// A dialog (an approval, the quit prompt) takes the content area only, so
// the menu and the header stay in view behind it.
// View hands the frame to Bubble Tea on the alternate screen, so the
// terminal's scrollback is left as it was when the sidecar stops.
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// render is the frame as text: the header and the key hints across the top,
// the section menu on the left and the section's content on the right.
func (m model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < 60 || m.height < 14 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			stFaint.Render("hoop sidecar: make the terminal at least 60×14"))
	}
	h := m.height - chrome
	mw := menuWidth(m.width)
	cw := m.width - mw
	var content string
	switch {
	case m.confirmQuit:
		content = m.confirmView(cw, h)
	case m.modal != "":
		content = m.approvalView(cw, h)
	default:
		content = m.content(cw, h)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.menu(mw, h), content)
	return lipgloss.JoinVertical(lipgloss.Left, m.header(), m.hints(), body)
}

func menuWidth(w int) int {
	if w >= 100 {
		return 22
	}
	return 18
}

// listHeight is the rows a list shows: the body less its border and title.
func (m model) listHeight() int { return max(m.height-chrome-3, 1) }

// header is two rows: who and how long on the left with the last minute's
// traffic on the right, then the totals that matter at a glance.
func (m model) header() string {
	now := m.now()
	state := m.spin.View() + " " + stPrimary.Render("live")
	switch {
	case m.done:
		state = stFaint.Render("■ stopped")
	case m.stopping:
		state = m.spin.View() + " " + stStrong.Render("stopping…")
	}
	left := stBrand.Render("hoop sidecar") + " " +
		stFaint.Render(m.version+" · up "+short(now.Sub(m.st.Started))) + "  " + state

	stmts, denied := m.st.Spark(now)
	rate := 0
	for _, v := range stmts[len(stmts)-10:] {
		rate += v
	}
	right := stFaint.Render("last 60s ") + sparkline(stmts, denied) + " " +
		stBold.Render(fmt.Sprintf("%.1f/s", float64(rate)/10))
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > m.width {
		right = ""
	}
	gap := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	line1 := left + strings.Repeat(" ", gap) + right

	open := len(m.st.OpenSessions())
	chips := []string{
		chip("●", num(open), "open", stPrimary, open > 0),
		chip("▸", num(m.st.Statements), "statements", stStrong, m.st.Statements > 0),
		chip("✕", num(m.st.Denied), "denied", stDanger, m.st.Denied > 0),
		chip("▒", num(m.st.Masked), "masked", stStrong, m.st.Masked > 0),
	}
	if n := m.st.PendingReviews(); n > 0 && m.tab != tabReviews {
		// The one moving thing in the header: something is waiting on a
		// person, and the hint says where to go.
		chips = append(chips, shimmer(fmt.Sprintf("⧗ %d awaiting approval · press 3", n), m.now()))
	} else if len(m.st.Reviews) > 0 {
		chips = append(chips, chip("⧗", num(n), "awaiting approval", stPrimary, n > 0))
	}
	if m.st.Errors > 0 {
		chips = append(chips, chip("!", num(m.st.Errors), "errors", stDanger, true))
	}
	if n := len(m.st.Warnings); n > 0 {
		chips = append(chips, chip("⚠", num(n), "warnings", stStrong, true))
	}
	line2 := ansi.Truncate(" "+strings.Join(chips, "   "), m.width, "…")
	return line1 + "\n" + line2
}

func chip(icon, n, label string, st lipgloss.Style, lit bool) string {
	if !lit {
		return stFaint.Render(icon + " " + n + " " + label)
	}
	return st.Render(icon) + " " + stBold.Render(n) + " " + stFaint.Render(label)
}

type keyHint [2]string

// hints is the row under the header saying what the keys do HERE: in this
// section, on this row, in this dialog. Enter is spelled out by what it
// does to the selected row, so nobody has to try it to find out.
func (m model) hints() string {
	if m.searching {
		return " " + m.search.View()
	}
	var keys []keyHint
	switch {
	case m.confirmQuit:
		keys = []keyHint{{"←→", "choose"}, {"enter", "confirm"}, {"y", "stop"}, {"n/esc", "keep running"}}
	case m.modal != "":
		keys = []keyHint{{"a", "approve"}, {"r", "reject"}, {"←→", "choose"}, {"enter", "confirm"}, {"esc", "close"}}
	case m.menuFocus:
		keys = []keyHint{{"↑↓", "choose a section"}, {"enter/→", "go in"}, {"/", "filter"}, {"q", "quit"}}
	case m.zoom:
		keys = []keyHint{{"esc", "back to the list"}, {"↑↓", "previous / next"}, {"←", "sections"}, {"q", "quit"}}
	case m.tab == tabTour && m.tour != nil:
		keys = m.tour.hints()
	case m.tab == tabSystem:
		keys = []keyHint{{"↑↓", "scroll"}, {"←/esc", "sections"}, {"q", "quit"}}
	default:
		keys = []keyHint{{"↑↓", "move"}}
		switch {
		case m.selectedLocalPending() != "":
			keys = append(keys, keyHint{"enter", "approve or reject"})
		case len(m.keys(m.tab)) > 0:
			keys = append(keys, keyHint{"enter", "open"})
		}
		keys = append(keys, keyHint{"/", "filter"})
		if m.tab == tabWire {
			if m.deniedOnly {
				keys = append(keys, keyHint{"d", "show all"})
			} else {
				keys = append(keys, keyHint{"d", "denied only"})
			}
		}
		keys = append(keys, keyHint{"←/esc", "sections"}, keyHint{"q", "quit"})
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, stKey.Bold(true).Render(k[0])+" "+stFaint.Render(k[1]))
	}
	line := " " + strings.Join(parts, stFaint.Render("  ·  "))

	var flags []string
	if m.tab == tabWire && m.deniedOnly {
		flags = append(flags, badge("denied only", colDanger))
	}
	if q := m.search.Value(); q != "" {
		flags = append(flags, badge("/"+q, colPrimary))
	}
	if len(flags) > 0 {
		f := strings.Join(flags, " ")
		gap := max(m.width-lipgloss.Width(line)-lipgloss.Width(f)-1, 1)
		line += strings.Repeat(" ", gap) + f
	}
	return ansi.Truncate(line, m.width, "…")
}

// content is the section on the right: a list beside the selected row's
// details when there is room for both, stacked when not, and the details
// alone once enter opened them.
func (m model) content(w, h int) string {
	if m.tab == tabTour && m.tour != nil {
		return pane("", m.tour.view(w-4, h-2, m.now()), w, h)
	}
	if m.tab == tabSystem {
		return pane("", m.systemView(w-4, h-2, m.cur[tabSystem].off), w, h)
	}
	keys := m.keys(m.tab)
	sel := indexOf(keys, m.cur[m.tab].sel)
	if m.zoom && len(keys) > 0 {
		return pane(m.listTitle(len(keys))+stFaint.Render(fmt.Sprintf("  ·  %d of %d", sel+1, len(keys))),
			m.detailView(keys, sel, w-4, h-3), w, h)
	}
	if w >= 100 {
		lw := w * 11 / 20
		list := pane(m.listTitle(len(keys)), m.list(keys, sel, lw-4, h-3), lw, h)
		det := pane("", m.detailView(keys, sel, w-lw-4, h-2), w-lw, h)
		return lipgloss.JoinHorizontal(lipgloss.Top, list, det)
	}
	lh := max(h*11/20, 5)
	list := pane(m.listTitle(len(keys)), m.list(keys, sel, w-4, lh-3), w, lh)
	det := pane("", m.detailView(keys, sel, w-4, h-lh-2), w, h-lh)
	return lipgloss.JoinVertical(lipgloss.Left, list, det)
}

// confirmView asks before stopping the sidecar. No is focused, and filled
// neutral gray as the safe answer; Yes is the danger answer, red.
func (m model) confirmView(w, h int) string {
	bw := min(max(w-4, 30), 64)
	noText, yesText := "No, keep running", "Yes, stop"
	if bw-4 < 36 {
		// The long labels would wrap the button row on a narrow screen.
		noText, yesText = "No", "Yes, stop"
	}
	no := stFaint.Bold(true).Padding(0, 2).Render(noText)
	yes := stDanger.Bold(true).Padding(0, 2).Render(yesText)
	if m.quitYes {
		yes = lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(colDanger).Padding(0, 2).Render(yesText)
	} else {
		no = lipgloss.NewStyle().Bold(true).Foreground(colStrong).Background(colNeutral).Padding(0, 2).Render(noText)
	}
	open := len(m.st.OpenSessions())
	body := []string{
		stStrong.Render("Stop the sidecar?"),
		"",
		lipgloss.NewStyle().Width(bw - 4).Render(fmt.Sprintf(
			"Every listener closes and %d open connection(s) end.", open)),
		"",
		lipgloss.PlaceHorizontal(bw-4, lipgloss.Right, no+"  "+yes),
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colDanger).
		Padding(0, 1).Width(bw).Render(strings.Join(body, "\n"))
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

func (m model) listTitle(n int) string {
	switch m.tab {
	case tabWire:
		return stTitle.Render("Through the wire") + stFaint.Render(fmt.Sprintf("  %d events", n))
	case tabSessions:
		return stTitle.Render("Connections") + stFaint.Render(fmt.Sprintf("  %d open · %d recent",
			len(m.st.OpenSessions()), n-len(m.st.OpenSessions())))
	case tabReviews:
		return stTitle.Render("Approvals") + stFaint.Render(fmt.Sprintf("  %d", n))
	case tabLanes:
		return stTitle.Render("Listeners") + stFaint.Render(fmt.Sprintf("  %d", n))
	case tabLogs:
		return stTitle.Render("Operational log") + stFaint.Render(fmt.Sprintf("  %d lines", n))
	}
	return ""
}

// pane draws a bordered box of exactly w×h cells.
func pane(title, content string, w, h int) string {
	inner := max(h-2, 1)
	lines := strings.Split(content, "\n")
	if title != "" {
		lines = append([]string{ansi.Truncate(title, w-4, "…")}, lines...)
	}
	if len(lines) > inner {
		lines = lines[:inner]
	}
	for len(lines) < inner {
		lines = append(lines, "")
	}
	return stPane.Width(w).Height(inner + 2).Render(strings.Join(lines, "\n"))
}

// withBackground paints the selection background under an already styled
// row, padded to width. Wrapping the row in a lipgloss style would not do:
// each colored segment inside ends with a reset, which clears the background
// from there on, so the row would show it only up to its first color. The
// background is re-applied after every reset instead.
func withBackground(row string, width int) string {
	pad := strings.Repeat(" ", max(width-ansi.StringWidth(row), 0))
	bg := selectionSequence()
	if bg == "" {
		return row + pad
	}
	// Both spellings of the reset: "\x1b[0m" and the shorter "\x1b[m".
	row = strings.ReplaceAll(row, "\x1b[0m", "\x1b[m")
	return bg + strings.ReplaceAll(row, "\x1b[m", "\x1b[m"+bg) + pad + "\x1b[m"
}

// selectionSequence is the escape sequence that sets the selection
// background, taken from what lipgloss renders for it so the two can never
// disagree. Bubble Tea downsamples it to the terminal's colors on output.
func selectionSequence() string {
	const mark = "\x00"
	painted := lipgloss.NewStyle().Background(colSelBg).Render(mark)
	if i := strings.Index(painted, mark); i > 0 {
		return painted[:i]
	}
	return ""
}

func (m model) empty(w int) string {
	var b strings.Builder
	switch m.tab {
	case tabWire:
		if m.search.Value() != "" || m.deniedOnly {
			b.WriteString(stFaint.Render("Nothing matches the filter. esc clears it."))
			break
		}
		b.WriteString(stFaint.Render("Waiting for traffic. Point a client at a listener:") + "\n\n")
		for _, name := range m.st.LaneOrder {
			l := m.st.Lanes[name]
			b.WriteString("  " + stStrong.Render(name) + "  " + protocolBadge(l.Protocol) + "  " +
				stBold.Render(orDash(l.Listen)) + stFaint.Render(" → "+orDash(l.Upstream)) + "\n")
		}
		if len(m.st.LaneOrder) == 0 {
			b.WriteString("  " + m.spin.View() + stFaint.Render(" starting listeners…"))
		}
	case tabSessions:
		b.WriteString(stFaint.Render("No connections yet."))
	case tabReviews:
		hint := "No approvals yet.\n\nA statement an analyzer holds for approval shows here.\n"
		if m.reviewer != nil {
			hint += "You decide it here: select it and press enter."
		} else {
			hint += "It is decided in the control plane."
		}
		b.WriteString(stFaint.Render(hint))
	case tabLanes:
		b.WriteString(m.spin.View() + stFaint.Render(" starting listeners…"))
	case tabLogs:
		b.WriteString(stFaint.Render("No log lines match."))
	}
	return lipgloss.NewStyle().Width(w).Render(b.String())
}

func clock(t time.Time) string {
	if t.IsZero() {
		return "--:--:--"
	}
	return t.Local().Format("15:04:05")
}

func col(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func laneMode(l *Lane) string {
	switch {
	case !l.Ready:
		return stFaint.Render(col("starting", 8))
	case l.Observing:
		return stText.Render(col("OBSERVE", 8))
	case l.Enforcing:
		return stPrimary.Render(col("ENFORCE", 8))
	}
	return stFaint.Render(col("AUDIT", 8))
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// kv is one "label  value" row of a detail pane.
type kv struct{ k, v string }

func kvBlock(rows []kv, w int) string {
	var out []string
	for _, r := range rows {
		if r.v == "" {
			continue
		}
		label := stLabel.Render(col(r.k, 12))
		val := lipgloss.NewStyle().Width(max(w-13, 10)).Render(r.v)
		out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, label, " ", val))
	}
	return strings.Join(out, "\n")
}

// codeBlock draws statement text under a rule, wrapped to the pane.
func codeBlock(title, text string, w int, accent color.Color) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	bar := lipgloss.NewStyle().Foreground(accent).Render("│ ")
	wrapped := lipgloss.NewStyle().Width(max(w-2, 10)).Render(strings.ReplaceAll(text, "\t", "    "))
	var lines []string
	for _, l := range strings.Split(wrapped, "\n") {
		lines = append(lines, bar+l)
	}
	return stLabel.Render(title) + "\n" + strings.Join(lines, "\n")
}

func sortedMeta(md map[string]string, skip ...string) []kv {
	keys := make([]string, 0, len(md))
	for k := range md {
		if !slices.Contains(skip, k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]kv, 0, len(keys))
	for _, k := range keys {
		out = append(out, kv{k, md[k]})
	}
	return out
}

func (m model) detailView(keys []string, sel, w, h int) string {
	if len(keys) == 0 {
		return stFaint.Render("Select a row to see its details.")
	}
	key := keys[sel]
	switch m.tab {
	case tabWire:
		i, _ := strconv.Atoi(key)
		return m.wireDetail(m.st.Feed[i-m.st.FeedDropped], w)
	case tabSessions:
		return m.sessionDetail(m.st.Sessions[key], w, h)
	case tabReviews:
		return reviewDetail(m.st.Reviews[key], w, m.now())
	case tabLanes:
		return m.laneDetail(m.st.Lanes[key], w)
	case tabLogs:
		i, _ := strconv.Atoi(key)
		return logDetail(m.st.Logs[i-m.st.LogsDropped], w)
	}
	return ""
}

func (m model) wireDetail(ev audit.Event, w int) string {
	var title string
	accent := color.Color(colFaint)
	switch ev.Kind {
	case audit.KindStatement:
		title = stStrong.Render("✓ Allowed")
	case audit.KindViolation:
		title = stDanger.Bold(true).Render("✕ Denied")
		accent = colDanger
	case audit.KindMasked:
		title = stStrong.Render("▒ Response masked")
		accent = colStrong
	case audit.KindError:
		title = stDanger.Bold(true).Render("! Error")
		accent = colDanger
	case audit.KindActivity:
		title = stStrong.Render("◆ Activity")
		accent = colFaint
	case audit.KindSessionStart:
		title = stBold.Render("→ Session opened")
	case audit.KindSessionEnd:
		title = stBold.Render("← Session closed")
	}
	rows := []kv{
		{"time", ev.Timestamp.Local().Format("2006-01-02 15:04:05.000")},
		{"listener", ev.Connection},
		{"protocol", string(ev.Protocol)},
		{"user", ev.Principal},
		{"session", string(ev.SessionID)},
		{"operation", strings.ToUpper(string(ev.Operation))},
		{"direction", string(ev.Direction)},
		{"tables", strings.Join(ev.Tables, ", ")},
	}
	if ev.Kind == audit.KindViolation {
		rows = append(rows, kv{"rule", stDanger.Render(ev.Rule)}, kv{"message", ev.Message})
	} else if ev.Message != "" {
		rows = append(rows, kv{"message", ev.Message})
	}
	if ev.Kind == audit.KindMasked {
		rows = append(rows, kv{"masked", fmt.Sprintf("%d value(s): %s", max(ev.MaskedCount, 1),
			strings.Join(ev.MaskedEntities, ", "))})
	}
	if ev.Error != "" {
		rows = append(rows, kv{"error", stDanger.Render(ev.Error)})
	}
	if ev.Kind == audit.KindSessionEnd {
		rows = append(rows, kv{"duration", short(ev.Duration)},
			kv{"statements", strconv.Itoa(ev.StatementCount)},
			kv{"denied", strconv.Itoa(ev.DeniedCount)})
	}
	if h := ev.HTTP; h != nil {
		req := strings.TrimSpace(h.Method + " " + h.Path)
		rows = append(rows, kv{"http", req}, kv{"host", h.Host}, kv{"resource", h.Resource})
		if h.StatusCode != 0 {
			rows = append(rows, kv{"status", strconv.Itoa(h.StatusCode)})
		}
	}

	// The analyzer's verdict gets its own block: it is the part of the
	// record an operator most often has to explain.
	var ai []kv
	if lvl := ev.Metadata[metaRiskLevel]; lvl != "" {
		ai = append(ai, kv{"risk", riskBadge(lvl) + " " + stFaint.Render(ev.Metadata[metaRiskAction])})
	}
	ai = append(ai, kv{"ai status", ev.Metadata[metaAIStatus]}, kv{"ai rule", ev.Metadata[metaAIRule]})
	if rid := ev.Metadata[metaReviewID]; rid != "" {
		ai = append(ai, kv{"approval", stPrimary.Render(rid) + " " + reviewBadge(reviewStatus(ev))},
			kv{"approval mode", ev.Metadata[metaReviewMode]})
	}

	parts := []string{title, "", kvBlock(rows, w)}
	if b := kvBlock(ai, w); b != "" {
		parts = append(parts, "", stLabel.Render("analyzer"), b)
	}
	if b := codeBlock("statement", ev.Statement, w, accent); b != "" {
		parts = append(parts, "", b)
	}
	meta := sortedMeta(ev.Metadata, metaRiskLevel, metaRiskAction, metaAIStatus, metaAIRule,
		metaReviewID, metaReviewMode)
	if len(meta) > 0 {
		parts = append(parts, "", stLabel.Render("metadata"), kvBlock(meta, w))
	}
	return strings.Join(parts, "\n")
}

func (m model) sessionDetail(s *Session, w, h int) string {
	if s == nil {
		return ""
	}
	now := m.now()
	state := stPrimary.Render("● Open connection")
	if !s.Open {
		state = stFaint.Bold(true).Render("○ Closed connection")
	}
	rows := []kv{
		{"session", s.ID},
		{"listener", s.Lane},
		{"protocol", s.Protocol},
		{"user", s.Principal},
		{"started", s.Started.Local().Format("15:04:05")},
		{"duration", short(s.Duration(now))},
		{"statements", strconv.Itoa(s.Statements)},
		{"denied", strconv.Itoa(s.Denied)},
		{"masked", strconv.Itoa(s.Masked)},
	}
	parts := []string{state, "", kvBlock(rows, w)}
	if len(s.Meta) > 0 {
		parts = append(parts, "", stLabel.Render("metadata"), kvBlock(sortedMeta(s.Meta), w))
	}
	var recent []string
	for i := len(m.st.Feed) - 1; i >= 0 && len(recent) < max(h-lipgloss.Height(strings.Join(parts, "\n"))-3, 3); i-- {
		ev := m.st.Feed[i]
		if string(ev.SessionID) == s.ID {
			recent = append(recent, ansi.Truncate(wireRow(ev), w, "…"))
		}
	}
	if len(recent) > 0 {
		parts = append(parts, "", stLabel.Render("recent activity"))
		parts = append(parts, recent...)
	}
	return strings.Join(parts, "\n")
}

func reviewDetail(r *Review, w int, now time.Time) string {
	if r == nil {
		return ""
	}
	rows := []kv{
		{"status", reviewBadge(r.Status)},
		{"risk", riskLine(r)},
		{"why", r.Why},
		{"analyzer", r.AIRule},
		{"approval", r.ID},
		{"listener", r.Lane},
		{"user", r.Principal},
		{"mode", r.Mode},
		{"first seen", r.First.Local().Format("15:04:05") + stFaint.Render(" ("+short(now.Sub(r.First))+" ago)")},
		{"last seen", r.Last.Local().Format("15:04:05")},
		{"attempts", strconv.Itoa(r.Hits)},
		{"message", r.Message},
	}
	accent := color.Color(colPrimary)
	switch r.Status {
	case "APPROVED":
		accent = colPrimary
	case "REJECTED", "REVOKED", "DENIED":
		accent = colDanger
	}
	where := "control plane"
	if r.Local {
		where = "this terminal"
	}
	rows = append(rows, kv{"decided in", where})
	if r.DecidedBy != "" {
		rows = append(rows, kv{"decided by", r.DecidedBy + stFaint.Render(" at "+r.Decided.Local().Format("15:04:05"))})
	}
	parts := []string{stBold.Render("⧗ Approval"), "", kvBlock(rows, w)}
	if b := codeBlock("statement", r.Statement, w, accent); b != "" {
		parts = append(parts, "", b)
	}
	if r.Status == "PENDING" {
		hint := "Approve or reject it in the control plane. The client resends the statement once it is approved."
		if r.Local {
			hint = "Press enter to approve or reject it here."
		}
		parts = append(parts, "", stFaint.Render(lipgloss.NewStyle().Width(w).Render(hint)))
	}
	return strings.Join(parts, "\n")
}

func (m model) laneDetail(l *Lane, w int) string {
	if l == nil {
		return ""
	}
	yes := func(b bool) string {
		if b {
			return stPrimary.Render("on")
		}
		return stFaint.Render("off")
	}
	rows := []kv{
		{"mode", laneMode(l)},
		{"protocol", l.Protocol},
		{"listen", l.Listen + stFaint.Render(" "+l.Network)},
		{"upstream", l.Upstream},
		{"rules", l.Rules},
		{"opa", orDash(l.OPA)},
		{"masking", yes(l.Masking)},
	}
	counters := []kv{
		{"active", strconv.Itoa(m.st.Active(l.Name))},
		{"sessions", strconv.Itoa(l.Sessions)},
		{"statements", strconv.Itoa(l.Statements)},
		{"denied", strconv.Itoa(l.Denied)},
		{"masked", strconv.Itoa(l.Masked)},
		{"errors", strconv.Itoa(l.Errors)},
	}
	parts := []string{stStrong.Render(l.Name), "", kvBlock(rows, w), "", stLabel.Render("traffic"), kvBlock(counters, w)}
	if len(l.Notes) > 0 {
		parts = append(parts, "", stLabel.Render("warnings"))
		for _, n := range l.Notes {
			parts = append(parts, stStrong.Render("⚠ ")+lipgloss.NewStyle().Width(max(w-2, 10)).Render(n))
		}
	}
	return strings.Join(parts, "\n")
}

func logDetail(r LogRecord, w int) string {
	rows := []kv{{"time", r.Time.Local().Format("2006-01-02 15:04:05.000")}, {"level", levelStyle(r.Level).Render(r.Level)}}
	for _, a := range r.Attrs {
		rows = append(rows, kv{a.Key, a.Value})
	}
	return strings.Join([]string{lipgloss.NewStyle().Bold(true).Width(w).Render(r.Msg), "", kvBlock(rows, w)}, "\n")
}

func (m model) systemView(w, h, off int) string {
	sys := m.st.System
	license := sys.License
	if sys.LicenseWarn {
		license = stDanger.Render(license)
	}
	on := func(b bool, text string) string {
		if b {
			return stPrimary.Render("● ") + text
		}
		return stFaint.Render("○ off")
	}
	rows := []kv{
		{"version", m.version},
		{"license", orDash(license)},
		{"rule limits", orDash(sys.Limits)},
		{"pii detector", on(sys.Detection, "attached")},
		{"ai analyzer", on(sys.Analyzer != "", sys.Analyzer)},
		{"control plane", on(sys.ControlPlane != "", sys.ControlPlane)},
		{"session sync", on(sys.SessionsSink, "sending session events to the control plane")},
		{"admin api", on(sys.Admin != "", sys.Admin)},
		{"query api", on(sys.QueryAPI, "/api/sessions · /api/events · /api/stats")},
		{"config watch", on(sys.Watching != "", sys.Watching+stFaint.Render(" · edits apply live, SIGHUP to force"))},
		{"analytics", on(sys.Analytics, "usage analytics enabled")},
	}
	for _, n := range m.notes {
		rows = append(rows, kv{"output", n})
	}
	if m.dropped > 0 {
		rows = append(rows, kv{"screen", stDanger.Render(fmt.Sprintf(
			"%s lines not shown: the screen fell behind (the audit trail has them)", num(int(m.dropped))))})
	}
	parts := []string{stTitle.Render("Sidecar"), "", kvBlock(rows, w)}

	if len(m.st.Risk) > 0 || len(m.st.AIStatus) > 0 {
		parts = append(parts, "", stTitle.Render("Analyzer verdicts"), "")
		var risk []string
		for _, lvl := range []string{"high", "medium", "low"} {
			if n := m.st.Risk[lvl]; n > 0 {
				risk = append(risk, riskBadge(lvl)+" "+stBold.Render(num(n)))
			}
		}
		if len(risk) > 0 {
			parts = append(parts, kvBlock([]kv{{"risk", strings.Join(risk, "   ")}}, w))
		}
		var st []kv
		for _, k := range sortedMeta(intMap(m.st.AIStatus)) {
			st = append(st, kv{k.k, k.v})
		}
		if len(st) > 0 {
			parts = append(parts, stLabel.Render("ai status"), kvBlock(st, w))
		}
	}

	parts = append(parts, "", stTitle.Render("Warnings and errors")+stFaint.Render(fmt.Sprintf("  %d", len(m.st.Warnings))), "")
	if len(m.st.Warnings) == 0 {
		parts = append(parts, stPrimary.Render("✓ ")+stFaint.Render("none"))
	}
	for i := len(m.st.Warnings) - 1; i >= 0; i-- {
		r := m.st.Warnings[i]
		parts = append(parts, ansi.Truncate(logRow(r), w, "…"))
	}
	lines := strings.Split(strings.Join(parts, "\n"), "\n")
	off = min(off, max(len(lines)-h, 0))
	return strings.Join(lines[off:], "\n")
}

func intMap(in map[string]int) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = num(v)
	}
	return out
}

// approvalView is the dialog a held statement opens: what is waiting, where,
// and the two answers. Approve is the primary action (blue) and Reject the
// danger one (red); the focused button is filled. Focus starts on Reject, so
// an enter pressed out of habit never releases a statement.
// approvalLayout is the dialog's geometry, shared by the view and the keys so
// "the whole statement has been shown" means the same thing to both:
// the fixed facts above, the statement wrapped to the dialog's width, and the
// rows the statement gets. On a short screen the facts shrink to the title,
// never the statement: its lines are only ever scrolled, not cut.
func (m model) approvalLayout() (r *Review, fixed, stmt []string, room, bw int) {
	w, h := m.width-menuWidth(m.width), m.height-chrome
	r = m.st.Reviews[m.modal]
	if r == nil {
		return nil, nil, nil, 1, w
	}
	bw = min(max(w-8, 40), 100, w)
	inner := bw - 4

	who := stFaint.Render("nobody connected")
	if p := m.st.Principals(r.Lane); len(p) > 0 {
		who = strings.Join(p, ", ")
	}
	proto := ""
	if l := m.st.Lanes[r.Lane]; l != nil && l.Protocol != "" {
		proto = stFaint.Render("  " + l.Protocol)
	}
	rows := []kv{
		{"risk", riskLine(r)},
		{"why", r.Why},
		{"listener", stStrong.Render(r.Lane) + proto},
		{"connected", who},
		{"waiting", short(m.now().Sub(r.First)) + stFaint.Render(" since "+r.First.Local().Format("15:04:05"))},
		{"you are", m.operator},
	}
	head := stPrimary.Render("⧗ Approval needed")
	id := stFaint.Render(r.ID)
	head += strings.Repeat(" ", max(inner-lipgloss.Width(head)-lipgloss.Width(id), 1)) + id

	// Below the statement: a scroll line, a blank, the buttons. Above it
	// and around it: the border.
	const below, border = 3, 2
	fixed = strings.Split(strings.Join([]string{head, "", kvBlock(rows, inner), "", stLabel.Render("statement")}, "\n"), "\n")
	room = h - border - len(fixed) - below
	if room < 3 {
		fixed = []string{head, stLabel.Render("statement")}
		room = h - border - len(fixed) - below
	}
	room = max(room, 1)

	bar := stPrimary.UnsetBold().Render("│ ")
	wrapped := lipgloss.NewStyle().Width(max(inner-2, 10)).Render(strings.ReplaceAll(r.Statement, "\t", "    "))
	for _, l := range strings.Split(wrapped, "\n") {
		stmt = append(stmt, bar+l)
	}
	return r, fixed, stmt, room, bw
}

func (m model) modalStatement() []string { _, _, stmt, _, _ := m.approvalLayout(); return stmt }
func (m model) modalRoom() int           { _, _, _, room, _ := m.approvalLayout(); return room }

// approvalView is the decision dialog: why the statement was held, where,
// the statement itself, and the two answers. A statement taller than the
// dialog scrolls, and Approve stays dimmed until its last line has been on
// screen, so nothing is released with an unread tail. Reject always works.
func (m model) approvalView(w, h int) string {
	r, fixed, stmt, room, bw := m.approvalLayout()
	if r == nil {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, "")
	}
	inner := bw - 4
	off := min(m.modalOff, max(len(stmt)-room, 0))
	visible := stmt[off:min(off+room, len(stmt))]

	scroll := ""
	if len(stmt) > room {
		scroll = stFaint.Render(fmt.Sprintf("lines %d–%d of %d · ↑↓ scroll", off+1, off+len(visible), len(stmt)))
		if !m.canApprove() {
			scroll += "  " + stStrong.Render("scroll to the end to approve")
		}
	}

	buttons := m.approvalButtons()
	more := ""
	if n := len(m.pendingLocal()) - 1; n > 0 {
		more = stFaint.Render(fmt.Sprintf("%d more waiting", n))
	}
	footer := lipgloss.PlaceHorizontal(inner, lipgloss.Right, buttons)
	if more != "" {
		footer = more + strings.Repeat(" ", max(inner-lipgloss.Width(more)-lipgloss.Width(buttons), 1)) + buttons
	}

	lines := append(append(append([]string{}, fixed...), visible...), scroll, "", footer)
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, inner, "…")
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colPrimary).
		Padding(0, 1).Width(bw).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

func (m model) approvalButtons() string {
	reject := stDanger.Bold(true).Padding(0, 2).Render("Reject")
	approve := stPrimary.Padding(0, 2).Render("Approve")
	switch {
	case !m.canApprove():
		// Dimmed, focused or not: the tail of the statement is unread.
		approve = stFaint.Padding(0, 2).Render("Approve")
		if !m.approveFocused {
			reject = lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(colDanger).Padding(0, 2).Render("Reject")
		}
	case m.approveFocused:
		approve = lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(colPrimary).Padding(0, 2).Render("Approve")
	default:
		reject = lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(colDanger).Padding(0, 2).Render("Reject")
	}
	return reject + "  " + approve
}

func (m model) pendingLocal() []string {
	var out []string
	for _, id := range m.st.ReviewOrder {
		if r := m.st.Reviews[id]; r != nil && r.Local && r.Status == statusPending {
			out = append(out, id)
		}
	}
	return out
}

// riskLine is the analyzer's verdict on one line: the level, then the model's
// title for it. Empty when the analyzer reported nothing.
func riskLine(r *Review) string {
	badge := riskBadge(r.Risk)
	switch {
	case badge == "" && r.Title == "":
		return ""
	case r.Title == "":
		return badge
	case badge == "":
		return stStrong.Render(r.Title)
	}
	return badge + "  " + stStrong.Render(r.Title)
}
