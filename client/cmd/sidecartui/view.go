package sidecartui

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// chrome is the rows the header, tab bar and footer take.
const chrome = 4

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < 60 || m.height < 12 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			stFaint.Render("hoop sidecar: make the terminal at least 60×12"))
	}
	body := m.body(m.width, m.height-chrome)
	return lipgloss.JoinVertical(lipgloss.Left, m.header(), m.tabs(), body, m.footer())
}

func (m model) listHeight() int { return max(m.height-chrome-2, 1) }

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
	if len(m.st.Reviews) > 0 {
		chips = append(chips, chip("⧗", num(m.st.PendingReviews()), "pending review", stPrimary, m.st.PendingReviews() > 0))
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

func (m model) tabs() string {
	parts := make([]string, 0, tabCount)
	for i := range tabCount {
		name := fmt.Sprintf("%d %s", i+1, tabNames[i])
		switch i {
		case tabSessions:
			if n := len(m.st.OpenSessions()); n > 0 {
				name += fmt.Sprintf(" (%d)", n)
			}
		case tabReviews:
			if n := m.st.PendingReviews(); n > 0 {
				name += fmt.Sprintf(" (%d)", n)
			}
		case tabLanes:
			if n := len(m.st.LaneOrder); n > 0 {
				name += fmt.Sprintf(" (%d)", n)
			}
		case tabSystem:
			if len(m.st.Warnings) > 0 {
				name += " ⚠"
			}
		}
		if i == m.tab {
			parts = append(parts, stTabOn.Render(name))
		} else {
			parts = append(parts, stTabOff.Render(name))
		}
	}
	line := " " + strings.Join(parts, "   ")
	var flags []string
	if m.tab == tabWire && m.deniedOnly {
		flags = append(flags, badge("denied only", colDanger))
	}
	if q := m.search.Value(); q != "" && !m.searching {
		flags = append(flags, badge("/"+q, colPrimary))
	}
	if !m.cur[m.tab].follow && m.tab != tabSystem && m.tab != tabLanes {
		flags = append(flags, stStrong.Render("paused · g to follow"))
	}
	if len(flags) > 0 {
		f := strings.Join(flags, " ")
		gap := max(m.width-lipgloss.Width(line)-lipgloss.Width(f)-1, 1)
		line += strings.Repeat(" ", gap) + f
	}
	return ansi.Truncate(line, m.width, "…")
}

func (m model) footer() string {
	if m.searching {
		return m.search.View()
	}
	keys := [][2]string{
		{"tab/1-6", "view"}, {"↑↓", "select"}, {"enter", "details"},
		{"/", "filter"}, {"f", "follow"},
	}
	if m.tab == tabWire {
		keys = append(keys, [2]string{"d", "denied"})
	}
	keys = append(keys, [2]string{"q", "stop sidecar"})
	var parts []string
	for _, k := range keys {
		parts = append(parts, stKey.Render(k[0])+" "+stFaint.Render(k[1]))
	}
	return ansi.Truncate(" "+strings.Join(parts, stFaint.Render(" · ")), m.width, "…")
}

// body splits the space between a list and the detail of its selection:
// side by side when the terminal is wide, stacked when it is not.
func (m model) body(w, h int) string {
	if m.tab == tabSystem {
		return pane("", m.systemView(w-4, h-2, m.cur[tabSystem].off), w, h)
	}
	keys := m.keys(m.tab)
	c := m.cur[m.tab]
	sel := indexOf(keys, c.sel, c.follow)

	if !m.detail {
		return pane(m.listTitle(len(keys)), m.list(keys, sel, w-4, h-3), w, h)
	}
	if w >= 110 {
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

func (m model) listTitle(n int) string {
	switch m.tab {
	case tabWire:
		return stTitle.Render("Through the wire") + stFaint.Render(fmt.Sprintf("  %d events", n))
	case tabSessions:
		return stTitle.Render("Connections") + stFaint.Render(fmt.Sprintf("  %d open · %d recent",
			len(m.st.OpenSessions()), n-len(m.st.OpenSessions())))
	case tabReviews:
		return stTitle.Render("Human reviews") + stFaint.Render(fmt.Sprintf("  %d", n))
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
	return stPane.Width(w - 2).Height(inner).Render(strings.Join(lines, "\n"))
}

// list renders the visible window of rows around the selection.
func (m model) list(keys []string, sel, w, h int) string {
	if len(keys) == 0 {
		return m.empty(w)
	}
	h = max(h, 1)
	off := 0
	if sel >= h {
		off = sel - h + 1
	}
	var rows []string
	for i := off; i < len(keys) && i < off+h; i++ {
		row := ansi.Truncate(m.row(keys[i], w), w, "…")
		if i == sel {
			row = stSel.Render(row + strings.Repeat(" ", max(w-lipgloss.Width(row), 0)))
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
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
		b.WriteString(stFaint.Render("No reviews yet.\n\nA statement an analyzer holds for human approval shows here\n" +
			"with its review id once the review settles. A lane in\nreview_mode \"return\" shows it at once, as PENDING."))
	case tabLanes:
		b.WriteString(m.spin.View() + stFaint.Render(" starting listeners…"))
	case tabLogs:
		b.WriteString(stFaint.Render("No log lines match."))
	}
	return lipgloss.NewStyle().Width(w).Render(b.String())
}

func (m model) row(key string, w int) string {
	switch m.tab {
	case tabWire:
		i, _ := strconv.Atoi(key)
		return wireRow(m.st.Feed[i-m.st.FeedDropped])
	case tabSessions:
		return sessionRow(m.st.Sessions[key], m.now())
	case tabReviews:
		return reviewRow(m.st.Reviews[key], m.now())
	case tabLanes:
		return m.laneRow(m.st.Lanes[key])
	case tabLogs:
		i, _ := strconv.Atoi(key)
		return logRow(m.st.Logs[i-m.st.LogsDropped])
	}
	return ""
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

func wireRow(ev audit.Event) string {
	parts := []string{
		stFaint.Render(clock(ev.Timestamp)),
		verdictBadge(string(ev.Kind), ev.Allowed),
		stText.Render(col(ev.Connection, 12)),
		col(ev.Principal, 16),
	}
	switch ev.Kind {
	case audit.KindStatement, audit.KindViolation:
		if ev.Direction == inspect.FromServer {
			// A response the upstream sent back: its operation is
			// unknown by construction, and the arrow says more.
			parts = append(parts, stFaint.Render(col("↩ RESP", 7)))
		} else {
			parts = append(parts, stBold.Render(col(strings.ToUpper(string(ev.Operation)), 7)))
		}
		if r := riskBadge(ev.Metadata[metaRiskLevel]); r != "" {
			parts = append(parts, r)
		}
		if rid := ev.Metadata[metaReviewID]; rid != "" {
			parts = append(parts, stPrimary.Render("⧗"))
		}
		text := oneLine(ev.Statement)
		if ev.HTTP != nil && text == "" {
			text = strings.TrimSpace(ev.HTTP.Method + " " + ev.HTTP.Path)
		}
		parts = append(parts, text)
		if ev.Kind == audit.KindViolation && ev.Rule != "" {
			parts = append(parts, stDanger.Render("["+ev.Rule+"]"))
		}
	case audit.KindMasked:
		parts = append(parts, stStrong.Render(fmt.Sprintf("%d value(s)", max(ev.MaskedCount, 1))),
			stFaint.Render(strings.Join(ev.MaskedEntities, ", ")))
	case audit.KindError:
		parts = append(parts, stDanger.Render(oneLine(ev.Error)))
	case audit.KindActivity:
		parts = append(parts, stText.Render(ev.Metadata[metaActivity]), stFaint.Render(oneLine(ev.Message)))
	case audit.KindSessionStart:
		parts = append(parts, stFaint.Render(protocolText(ev)+" session "+shortID(string(ev.SessionID))))
	case audit.KindSessionEnd:
		parts = append(parts, stFaint.Render(fmt.Sprintf("after %s · %d statements · %d denied",
			short(ev.Duration), ev.StatementCount, ev.DeniedCount)))
	}
	return strings.Join(parts, " ")
}

func protocolText(ev audit.Event) string { return string(ev.Protocol) }

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func sessionRow(s *Session, now time.Time) string {
	if s == nil {
		return ""
	}
	state := stPrimary.Render("● open  ")
	if !s.Open {
		state = stFaint.Render("○ closed")
	}
	// Counts as icon columns, the header's vocabulary: ▸ statements,
	// ✕ denied, ▒ masked. Words would push the last statement off a
	// 120-column screen, and it is the column that says what happened.
	counts := func(n int, icon string, lit lipgloss.Style) string {
		s := fmt.Sprintf("%3d%s", n, icon)
		if n == 0 {
			return stFaint.Render(s)
		}
		return lit.Render(s)
	}
	return strings.Join([]string{
		state,
		stText.Render(col(s.Lane, 12)),
		col(s.Principal, 14),
		protocolBadge(col(s.Protocol, 6)),
		stFaint.Render(fmt.Sprintf("%6s", short(s.Duration(now)))),
		counts(s.Statements, "▸", stBold),
		counts(s.Denied, "✕", stDanger),
		counts(s.Masked, "▒", stStrong),
		stFaint.Render(oneLine(s.Last)),
	}, " ")
}

func reviewRow(r *Review, now time.Time) string {
	if r == nil {
		return ""
	}
	return strings.Join([]string{
		col(reviewBadge(r.Status), 11),
		stBold.Render(col(r.ID, 14)),
		stText.Render(col(r.Lane, 12)),
		col(r.Principal, 16),
		stFaint.Render(fmt.Sprintf("%7s ago", short(now.Sub(r.Last)))),
		oneLine(r.Statement),
	}, " ")
}

func (m model) laneRow(l *Lane) string {
	if l == nil {
		return ""
	}
	dot := stFaint.Render("○")
	if l.Ready {
		dot = stPrimary.Render("●")
	}
	active := stFaint.Render(fmt.Sprintf("%2d●", m.st.Active(l.Name)))
	if n := m.st.Active(l.Name); n > 0 {
		active = stPrimary.Render(fmt.Sprintf("%2d●", n))
	}
	return strings.Join([]string{
		dot,
		stStrong.Render(col(l.Name, 12)),
		laneMode(l),
		active,
		protocolBadge(col(l.Protocol, 8)),
		orDash(l.Listen),
		stFaint.Render("→ " + orDash(l.Upstream)),
	}, " ")
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

func logRow(r LogRecord) string {
	var attrs []string
	for _, a := range r.Attrs {
		attrs = append(attrs, a.Key+"="+quoteValue(a.Value))
	}
	return stFaint.Render(clock(r.Time)) + " " + levelStyle(r.Level).Render(col(r.Level, 5)) + " " +
		r.Msg + " " + stFaint.Render(strings.Join(attrs, " "))
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
func codeBlock(title, text string, w int, color lipgloss.TerminalColor) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	bar := lipgloss.NewStyle().Foreground(color).Render("│ ")
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
	color := lipgloss.TerminalColor(colFaint)
	switch ev.Kind {
	case audit.KindStatement:
		title = stStrong.Render("✓ Allowed")
	case audit.KindViolation:
		title = stDanger.Bold(true).Render("✕ Denied")
		color = colDanger
	case audit.KindMasked:
		title = stStrong.Render("▒ Response masked")
		color = colStrong
	case audit.KindError:
		title = stDanger.Bold(true).Render("! Error")
		color = colDanger
	case audit.KindActivity:
		title = stStrong.Render("◆ Activity")
		color = colFaint
	case audit.KindSessionStart:
		title = stBold.Render("→ Session opened")
	case audit.KindSessionEnd:
		title = stBold.Render("← Session closed")
	}
	rows := []kv{
		{"time", ev.Timestamp.Local().Format("2006-01-02 15:04:05.000")},
		{"listener", ev.Connection},
		{"protocol", string(ev.Protocol)},
		{"principal", ev.Principal},
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
		ai = append(ai, kv{"review", stPrimary.Render(rid) + " " + reviewBadge(reviewStatus(ev))},
			kv{"review mode", ev.Metadata[metaReviewMode]})
	}

	parts := []string{title, "", kvBlock(rows, w)}
	if b := kvBlock(ai, w); b != "" {
		parts = append(parts, "", stLabel.Render("analyzer"), b)
	}
	if b := codeBlock("statement", ev.Statement, w, color); b != "" {
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
		{"principal", s.Principal},
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
		{"review", r.ID},
		{"listener", r.Lane},
		{"principal", r.Principal},
		{"mode", r.Mode},
		{"first seen", r.First.Local().Format("15:04:05") + stFaint.Render(" ("+short(now.Sub(r.First))+" ago)")},
		{"last seen", r.Last.Local().Format("15:04:05")},
		{"attempts", strconv.Itoa(r.Hits)},
		{"message", r.Message},
	}
	color := lipgloss.TerminalColor(colPrimary)
	switch r.Status {
	case "APPROVED":
		color = colPrimary
	case "REJECTED", "REVOKED", "DENIED":
		color = colDanger
	}
	parts := []string{stBold.Render("⧗ Human review"), "", kvBlock(rows, w)}
	if b := codeBlock("statement", r.Statement, w, color); b != "" {
		parts = append(parts, "", b)
	}
	if r.Status == "PENDING" {
		parts = append(parts, "", stFaint.Render(lipgloss.NewStyle().Width(w).Render(
			"Approve or reject it in the control plane. The client resends the statement once it is approved.")))
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
