package sidecartui

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// column is one column of a section's table: its header and its width in
// cells. A width of 0 takes what the others leave.
type column struct {
	title string
	width int
}

// columns are the tables' columns, one set per section. Each width counts the
// gap after the column, so a selected row's background runs unbroken across
// the gaps.
func (m model) columns() []column {
	switch m.tab {
	case tabWire:
		return []column{{"TIME", 9}, {"VERDICT", 9}, {"LISTENER", 13}, {"USER", 17}, {"WHAT WENT THROUGH", 0}}
	case tabSessions:
		return []column{{"STATE", 9}, {"LISTENER", 13}, {"USER", 15}, {"PROTO", 7}, {"  TIME", 7},
			{"STMT", 5}, {"DENY", 5}, {"MASK", 5}, {"LAST STATEMENT", 0}}
	case tabReviews:
		return []column{{"STATUS", 12}, {"RISK", 6}, {"APPROVAL", 15}, {"LISTENER", 13},
			{"USER", 17}, {"LAST SEEN", 12}, {"STATEMENT", 0}}
	case tabLanes:
		return []column{{"", 2}, {"NAME", 13}, {"MODE", 9}, {"CONN", 5}, {"PROTOCOL", 9}, {"LISTEN → UPSTREAM", 0}}
	case tabLogs:
		return []column{{"TIME", 9}, {"LEVEL", 6}, {"MESSAGE", 0}}
	}
	return nil
}

// cells are one row's values for the current section, in column order.
func (m model) cells(key string) []string {
	switch m.tab {
	case tabWire:
		i, _ := strconv.Atoi(key)
		return wireCells(m.st.Feed[i-m.st.FeedDropped])
	case tabSessions:
		return sessionCells(m.st.Sessions[key], m.now())
	case tabReviews:
		return reviewCells(m.st.Reviews[key], m.now())
	case tabLanes:
		return m.laneCells(m.st.Lanes[key])
	case tabLogs:
		i, _ := strconv.Atoi(key)
		return logCells(m.st.Logs[i-m.st.LogsDropped])
	}
	return nil
}

// list renders a section's rows as a Bubbles table: the column header first,
// so a newcomer reads what each column is before reading any row, then the
// rows around the selection.
//
// The table is built for each frame from the model's own selection, which is
// keyed by row rather than by index, so a selected row stays selected while
// new ones arrive above it. The selected row gets the soft background across
// its whole width, and a blue bar when the arrows are in the section.
func (m model) list(keys []string, sel, w, h int) string {
	if len(keys) == 0 {
		return m.empty(w)
	}
	specs := m.columns()
	cols := make([]table.Column, 0, len(specs)+1)
	cols = append(cols, table.Column{Title: "", Width: 1}) // the selection bar
	used := 1
	for _, c := range specs {
		width := c.width
		if width == 0 {
			width = max(w-used, 4)
		}
		used += width
		cols = append(cols, table.Column{Title: c.title, Width: width})
	}

	rows := make([]table.Row, len(keys))
	for i, k := range keys {
		cells := m.cells(k)
		row := make(table.Row, 0, len(cols))
		switch {
		case i == sel && !m.menuFocus:
			row = append(row, stPrimary.Background(colSelBg).Render("▌"))
		case i == sel:
			// The arrows are in the menu: the row keeps its background, so
			// the details beside it still say which row they describe.
			row = append(row, withBackground("", 1))
		default:
			row = append(row, " ")
		}
		for j, c := range cells {
			width := cols[j+1].Width
			text := ansi.Truncate(c, max(width-1, 1), "…")
			if i == sel {
				text = withBackground(text, width)
			}
			row = append(row, text)
		}
		rows[i] = row
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithWidth(w),
		table.WithHeight(max(h, 2)),
		table.WithStyles(table.Styles{
			Header:   stLabel.Bold(true),
			Cell:     lipgloss.NewStyle(),
			Selected: lipgloss.NewStyle(),
		}),
	)
	// From the top, MoveDown scrolls the way a person pressing down would,
	// which keeps the selected row inside the window.
	t.MoveDown(sel)
	// On a narrow screen the fixed columns are wider than the pane, and the
	// table does not clip; each line is cut to the pane here, so the right
	// columns go off the edge instead of wrapping onto the next row.
	lines := strings.Split(t.View(), "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "")
	}
	return strings.Join(lines, "\n")
}

func wireCells(ev audit.Event) []string {
	cells := []string{
		stFaint.Render(clock(ev.Timestamp)),
		verdictBadge(string(ev.Kind), ev.Allowed),
		stText.Render(ev.Connection),
		ev.Principal,
	}
	var what []string
	switch ev.Kind {
	case audit.KindStatement, audit.KindViolation:
		if ev.Direction == inspect.FromServer {
			// A response the upstream sent back: its operation is
			// unknown by construction, and the arrow says more.
			what = append(what, stFaint.Render(col("↩ RESP", 7)))
		} else {
			what = append(what, stBold.Render(col(strings.ToUpper(string(ev.Operation)), 7)))
		}
		if r := riskBadge(ev.Metadata[metaRiskLevel]); r != "" {
			what = append(what, r)
		}
		if rid := ev.Metadata[metaReviewID]; rid != "" {
			what = append(what, stPrimary.Render("⧗"))
		}
		text := oneLine(ev.Statement)
		if ev.HTTP != nil && text == "" {
			text = strings.TrimSpace(ev.HTTP.Method + " " + ev.HTTP.Path)
		}
		what = append(what, text)
		if ev.Kind == audit.KindViolation && ev.Rule != "" {
			what = append(what, stDanger.Render("["+ev.Rule+"]"))
		}
	case audit.KindMasked:
		what = append(what, stStrong.Render(fmt.Sprintf("%d value(s)", max(ev.MaskedCount, 1))),
			stFaint.Render(strings.Join(ev.MaskedEntities, ", ")))
	case audit.KindError:
		what = append(what, stDanger.Render(oneLine(ev.Error)))
	case audit.KindActivity:
		what = append(what, stText.Render(ev.Metadata[metaActivity]), stFaint.Render(oneLine(ev.Message)))
	case audit.KindSessionStart:
		what = append(what, stFaint.Render(string(ev.Protocol)+" session "+shortID(string(ev.SessionID))))
	case audit.KindSessionEnd:
		what = append(what, stFaint.Render(fmt.Sprintf("after %s · %d statements · %d denied",
			short(ev.Duration), ev.StatementCount, ev.DeniedCount)))
	}
	return append(cells, strings.Join(what, " "))
}

// wireRow is one wire event on a single line, for the places that list a few
// of them without a table (a session's recent activity).
func wireRow(ev audit.Event) string {
	c := wireCells(ev)
	return strings.Join([]string{col(c[0], 8), col(c[1], 8), col(c[2], 12), col(c[3], 16), c[4]}, " ")
}

func sessionCells(s *Session, now time.Time) []string {
	if s == nil {
		return nil
	}
	state := stPrimary.Render("● open")
	if !s.Open {
		state = stFaint.Render("○ closed")
	}
	// Counts as icons, the header's vocabulary: ▸ statements, ✕ denied,
	// ▒ masked; the column headers spell them out.
	counts := func(n int, icon string, lit lipgloss.Style) string {
		s := fmt.Sprintf("%3d%s", n, icon)
		if n == 0 {
			return stFaint.Render(s)
		}
		return lit.Render(s)
	}
	return []string{
		state,
		stText.Render(s.Lane),
		s.Principal,
		protocolBadge(s.Protocol),
		stFaint.Render(fmt.Sprintf("%6s", short(s.Duration(now)))),
		counts(s.Statements, "▸", stBold),
		counts(s.Denied, "✕", stDanger),
		counts(s.Masked, "▒", stStrong),
		stFaint.Render(oneLine(s.Last)),
	}
}

func reviewCells(r *Review, now time.Time) []string {
	if r == nil {
		return nil
	}
	risk := riskBadge(r.Risk)
	if risk == "" {
		risk = stFaint.Render("—")
	}
	return []string{
		reviewBadge(r.Status),
		risk,
		stBold.Render(r.ID),
		stText.Render(r.Lane),
		r.Principal,
		stFaint.Render(short(now.Sub(r.Last)) + " ago"),
		oneLine(r.Statement),
	}
}

func (m model) laneCells(l *Lane) []string {
	if l == nil {
		return nil
	}
	dot := stFaint.Render("○")
	if l.Ready {
		dot = stPrimary.Render("●")
	}
	active := stFaint.Render(fmt.Sprintf("%3d●", m.st.Active(l.Name)))
	if n := m.st.Active(l.Name); n > 0 {
		active = stPrimary.Render(fmt.Sprintf("%3d●", n))
	}
	return []string{
		dot,
		stStrong.Render(l.Name),
		laneMode(l),
		active,
		protocolBadge(l.Protocol),
		orDash(l.Listen) + stFaint.Render(" → "+orDash(l.Upstream)),
	}
}

func logCells(r LogRecord) []string {
	var attrs []string
	for _, a := range r.Attrs {
		attrs = append(attrs, a.Key+"="+quoteValue(a.Value))
	}
	return []string{
		stFaint.Render(clock(r.Time)),
		levelStyle(r.Level).Render(r.Level),
		r.Msg + " " + stFaint.Render(strings.Join(attrs, " ")),
	}
}

// logRow is one log record on a single line, for the System section.
func logRow(r LogRecord) string {
	c := logCells(r)
	return c[0] + " " + col(c[1], 5) + " " + c[2]
}

// section is one entry of the side menu.
type section struct{ tab tab }

func (s section) FilterValue() string { return tabNames[s.tab] }

// menuDelegate draws the side menu's entries. The lines are prepared by the
// model, which knows the counts, the focus and the clock the shimmer runs on;
// the list only places them.
type menuDelegate struct{ lines []string }

func (d menuDelegate) Height() int                         { return 1 }
func (d menuDelegate) Spacing() int                        { return 0 }
func (d menuDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d menuDelegate) Render(w io.Writer, _ list.Model, index int, _ list.Item) {
	if index >= 0 && index < len(d.lines) {
		_, _ = io.WriteString(w, d.lines[index])
	}
}

// menu is the section list on the left, a Bubbles list. The current section
// has the soft selection background, and a blue bar while the arrows are in
// the menu; a section with something waiting on a person (an approval)
// shimmers until the person goes there.
func (m model) menu(w, h int) string {
	inner := w - 4
	items := make([]list.Item, 0, tabCount)
	lines := make([]string, 0, tabCount)
	for i := range m.numTabs() {
		items = append(items, section{i})
		label := fmt.Sprintf("%d %s", i+1, tabNames[i])
		count := ""
		switch i {
		case tabSessions:
			if n := len(m.st.OpenSessions()); n > 0 {
				count = num(n)
			}
		case tabReviews:
			// Under a control plane the reviews are decided there, so
			// nothing here is waiting on this person: no count, and no
			// shimmer asking them to come.
			if n := m.st.PendingReviews(); n > 0 && m.controlPlane == "" {
				count = num(n)
			}
			if m.controlPlane != "" {
				count = "↗"
			}
		case tabLanes:
			if n := len(m.st.LaneOrder); n > 0 {
				count = num(n)
			}
		case tabSystem:
			if len(m.st.Warnings) > 0 {
				count = "⚠"
			}
		case tabTour:
			done := 0
			for _, d := range m.tour.done {
				if d {
					done++
				}
			}
			count = fmt.Sprintf("%d/%d", done, len(m.tour.done))
		}
		gap := strings.Repeat(" ", max(inner-1-lipgloss.Width(label)-lipgloss.Width(count), 1))
		text := label + gap + count
		// Approvals belong to the control plane when there is one: the
		// entry stays, dimmed, so the person can still go there and read
		// where they are handled.
		dim := i == tabReviews && m.controlPlane != ""
		switch {
		case dim && i == m.tab && m.menuFocus:
			lines = append(lines, stPrimary.Background(colSelBg).Render("▌")+
				stFaint.Background(colSelBg).Width(inner-1).Render(text))
		case dim && i == m.tab:
			lines = append(lines, " "+stFaint.Background(colSelBg).Width(inner-1).Render(text))
		case dim:
			lines = append(lines, " "+stFaint.Render(text))
		case i == m.tab && m.menuFocus:
			lines = append(lines, stPrimary.Background(colSelBg).Render("▌")+
				stPrimary.Background(colSelBg).Width(inner-1).Render(text))
		case i == m.tab:
			// The section the content shows, while the arrows are in it:
			// marked, without the bar that says "the arrows move here".
			lines = append(lines, " "+stStrong.Background(colSelBg).Width(inner-1).Render(text))
		case i == tabReviews && count != "":
			lines = append(lines, " "+shimmer(text, m.now()))
		default:
			lines = append(lines, " "+stText.Render(label)+gap+stFaint.Render(count))
		}
	}

	// As tall as the pane allows: a list exactly as tall as its items still
	// pages, and the menu must show every section at once.
	l := list.New(items, menuDelegate{lines: lines}, inner, max(h-4, 2*int(tabCount)))
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.Select(int(m.tab))

	content := lipgloss.JoinVertical(lipgloss.Left, stLabel.Render("SECTIONS"), "", l.View())
	return pane("", content, w, h)
}
