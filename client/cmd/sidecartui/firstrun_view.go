package sidecartui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// wordmark is "hoop" in block letters. The solid cells carry the brand blue
// and the shimmer; the outline cells stay faint, so the band reads as light
// moving across the letters rather than across a box.
var wordmark = []string{
	"██╗  ██╗ ██████╗  ██████╗ ██████╗ ",
	"██║  ██║██╔═══██╗██╔═══██╗██╔══██╗",
	"███████║██║   ██║██║   ██║██████╔╝",
	"██╔══██║██║   ██║██║   ██║██╔═══╝ ",
	"██║  ██║╚██████╔╝╚██████╔╝██║     ",
	"╚═╝  ╚═╝ ╚═════╝  ╚═════╝ ╚═╝     ",
}

// The band's two brighter steps over the brand blue. Light and dark
// terminals need opposite directions: lighter reads as a glint on black,
// deeper reads as one on white.
var (
	colGlint = shade(lipgloss.Color("#1F3A9E"), lipgloss.Color("#DCE3FF"))
	colGlow  = shade(lipgloss.Color("#2E4FC4"), lipgloss.Color("#8AA0EE"))
)

// frWordmark paints the wordmark with a diagonal band sweeping left to
// right, about once every two seconds. The frame is a function of the clock,
// like shimmer, so every redraw moves it and nothing keeps a timer.
func frWordmark(now time.Time) string {
	width := len([]rune(wordmark[0])) + len(wordmark)
	period := width + 14
	head := int(now.UnixMilli()/55) % period
	glint := lipgloss.NewStyle().Foreground(colGlint).Bold(true)
	glow := lipgloss.NewStyle().Foreground(colGlow).Bold(true)
	base := lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	rows := make([]string, len(wordmark))
	for y, line := range wordmark {
		var b strings.Builder
		for x, r := range []rune(line) {
			cell := string(r)
			if r != '█' {
				b.WriteString(stFaint.Render(cell))
				continue
			}
			switch d := head - (x + y); {
			case d == 0 || d == 1:
				b.WriteString(glint.Render(cell))
			case d >= 2 && d <= 4:
				b.WriteString(glow.Render(cell))
			default:
				b.WriteString(base.Render(cell))
			}
		}
		rows[y] = b.String()
	}
	return strings.Join(rows, "\n")
}

// QuickstartURL is the guide the welcome page sends a reader to.
const QuickstartURL = "https://hoop.dev/docs/introduction/quickstart"

// The flow diagram's fixed parts, in cells: the three nodes and the spaces
// around both arrows. Each arrow is frSegment(width) dashes plus its head.
const (
	frNodeClient   = len("o Client")
	frNodeSidecar  = len("o sidecar")
	frNodeResource = len("o any resource")
	frFlowFixed    = frNodeClient + frNodeSidecar + frNodeResource + 4 + 2
)

// frSegment is the length of each arrow: as long as 16, and shorter on a
// narrow terminal so the row never wraps.
func frSegment(width int) int {
	return max(min((width-4-frFlowFixed)/2, 16), 3)
}

// frAmbient is how long the idle dot takes to cross the diagram. It keeps
// the picture of traffic moving while nothing is sent.
const frAmbient = 2400 * time.Millisecond

// frFlow draws Client -> sidecar -> any resource, what the sidecar is for,
// with a dot crossing it: one always, on a slow loop, and one per request
// the default listener answered.
func (m firstRunModel) frFlow(now time.Time) string {
	seg := frSegment(m.width)
	cells := make([]string, 2*seg)
	for i := range cells {
		cells[i] = stFaint.Render("─")
	}
	dot := lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	place := func(f float64) {
		if f < 0 || f >= 1 {
			return
		}
		pos := int(f * float64(len(cells)))
		cells[pos] = dot.Render("●")
		if pos > 0 {
			cells[pos-1] = stKey.Render("•")
		}
	}
	place(float64(now.UnixMilli()%frAmbient.Milliseconds()) / float64(frAmbient.Milliseconds()))
	for _, p := range m.pulses {
		place(float64(now.Sub(p)) / float64(pulseLife))
	}
	arrow := func(c []string) string { return strings.Join(c, "") + stFaint.Render("▶") }
	node := func(icon, label string) string { return stKey.Render(icon) + " " + stStrong.Render(label) }
	row := node("◉", "Client") + " " + arrow(cells[:seg]) + " " +
		node("◆", "sidecar") + " " + arrow(cells[seg:]) + " " + node("◎", "any resource")
	return row + "\n\n" + stFaint.Render("See documentation: ") + stPrimary.Underline(true).Render(QuickstartURL)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// homeCards draws the Get started list as one bordered card per choice,
// with space between them. The selected card keeps the › marker and takes
// the brand border and the selection background, so it reads as the one
// enter acts on. A note row is a small heading between groups.
//
// It returns blocks, one per card or heading with its spacing, and the
// selected card's index, so a short terminal scrolls by whole cards and
// never shows half a border.
func (m firstRunModel) homeCards(w int) (blocks [][]string, sel int) {
	inner := max(w-4, 10)
	// File names share one column, so their details line up.
	nameW := 0
	for _, it := range m.home.items {
		if strings.HasPrefix(it.id, "file:") {
			nameW = max(nameW, ansi.StringWidth(it.label))
		}
	}
	for i, it := range m.home.items {
		if it.note {
			blocks = append(blocks, []string{"", "  " + stLabel.Render(it.label)})
			continue
		}
		on := i == m.home.cur
		name := it.label
		if strings.HasPrefix(it.id, "file:") {
			name = fmt.Sprintf("%-*s", nameW, name)
		}
		mark, label := "  ", stText.Bold(true).Render(name)
		if on {
			mark, label = stPrimary.Render("› "), stPrimary.Render(name)
		}
		// The two actions get their explanation on a line of its own; a
		// file is one line, so a folder of configs stays scannable.
		lines := []string{mark + label + "  " + stFaint.Render(it.detail)}
		if !strings.HasPrefix(it.id, "file:") {
			lines = []string{mark + label, "  " + stFaint.Render(it.detail)}
		}
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1).Width(w)
		for j, l := range lines {
			lines[j] = ansi.Truncate(l, inner, "…")
			if on {
				lines[j] = withBackground(lines[j], inner)
			}
		}
		if on {
			box = box.BorderForeground(colPrimary).Background(colSelBg)
			sel = len(blocks)
		}
		block := strings.Split(box.Render(strings.Join(lines, "\n")), "\n")
		if len(blocks) > 0 {
			block = append([]string{""}, block...)
		}
		blocks = append(blocks, block)
	}
	return blocks, sel
}

// fitBlocks joins the blocks that fit in h rows, keeping block sel in view
// and starting as near the top as that allows.
func fitBlocks(blocks [][]string, sel, h int) string {
	size := func(from, to int) int {
		n := 0
		for _, b := range blocks[from : to+1] {
			n += len(b)
		}
		return n
	}
	start := 0
	for start < sel && size(start, sel) > h {
		start++
	}
	end := sel
	for end+1 < len(blocks) && size(start, end+1) <= h {
		end++
	}
	var rows []string
	for _, b := range blocks[start : end+1] {
		rows = append(rows, b...)
	}
	// A block cut from the top keeps its card whole by dropping the gap.
	if len(rows) > 0 && rows[0] == "" {
		rows = rows[1:]
	}
	return strings.Join(rows, "\n")
}

// getStarted is the Get started panel: the home list, or the file picker
// while it is open, with what the last choice came to under it.
func (m firstRunModel) getStarted(w, h int, now time.Time) (title, body string) {
	var status []string
	switch {
	case m.checking != "":
		status = append(status, shimmer("checking "+filepath.Base(m.checking)+"…", now))
	case m.invalid != "":
		name := filepath.Base(m.invalid)
		status = append(status,
			stDanger.Bold(true).Render("✕ "+name+" is not a valid sidecar config."),
			stText.Render("Pick another file, or set one up. To see what is wrong:"),
			stKey.Render(ansi.Truncate("  hoop start sidecar --validate --config "+shortPath(m.invalid), w, "…")))
	case m.saved != "":
		status = append(status, stPrimary.Render("✓ saved "+m.saved)+stFaint.Render("  it is in the list above"))
	}
	room := max(h-len(status)-1, 3)
	if m.picker != nil {
		title, body = "Open a config file", m.picker.view(w, room)
	} else {
		blocks, sel := m.homeCards(w)
		title, body = "Get started", fitBlocks(blocks, sel, room)
	}
	if len(status) > 0 {
		body += "\n\n" + strings.Join(status, "\n")
	}
	return title, body
}

func (m firstRunModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m firstRunModel) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < 60 || m.height < 14 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			ansiClamp(stFaint.Render("hoop sidecar: make the terminal at least 60×14"), m.width))
	}
	if m.wiz != nil {
		return m.wizardFrame()
	}
	now := m.now()
	w := min(m.width-2, 92)

	var head string
	if m.width >= 70 && m.height >= 34 {
		head = frWordmark(now) + "\n" + stStrong.Render("hoop sidecar")
	} else {
		head = stBrand.Render("hoop") + " " + stStrong.Render("sidecar")
	}

	// Wrapped to the frame, so a narrow terminal breaks the sentence
	// instead of cutting it.
	wrap := lipgloss.NewStyle().Width(w).Align(lipgloss.Center)
	intro := wrap.Inherit(stText).Render("Welcome to hoop sidecar. Let's get it running: " +
		"set up a config in a few steps, or start from one you already have.")
	flow := m.frFlow(now)

	// The panel takes what the screen has left under the header, at most
	// what it needs: the picker can list a long folder.
	headH := lipgloss.Height(head) + 1 + lipgloss.Height(intro) + 1 + lipgloss.Height(flow) + 1 + 1 + 1
	room := max(m.height-headH, 6)
	var body string
	if m.picker != nil {
		title, gs := m.getStarted(w-4, room-2, now)
		body = pane(stTitle.Render(title), gs, w, min(strings.Count(gs, "\n")+4, room))
	} else {
		title, gs := m.getStarted(w, room-1, now)
		body = lipgloss.NewStyle().Width(w).Render(stTitle.Render(title) + "\n" + gs)
	}

	k := func(key, what string) string { return stKey.Render(key) + stFaint.Render(" "+what+"   ") }
	hints := k("↑↓", "move") + k("enter", "choose") + k("w", "set up") + k("o", "open a file") + k("q", "quit")
	if m.picker != nil {
		hints = k("type", "a path") + k("↑↓", "move") + k("tab/→", "into a folder") + k("enter", "open") + k("esc", "back")
	}
	if m.stopping {
		hints = stStrong.Render("stopping…") + stFaint.Render("  q again to leave now")
	}

	blocks := []string{head, "", intro, "", flow, "", body, "", hints}
	page := lipgloss.JoinVertical(lipgloss.Center, blocks...)
	// A frame taller than the terminal would scroll the top away; cut it
	// at the bottom instead, where only the hints live.
	if lines := strings.Split(page, "\n"); len(lines) > m.height {
		page = strings.Join(lines[:m.height], "\n")
	}
	page = ansiClamp(page, m.width)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, page)
}

// wizardFrame draws the setup screens: a header with where the person is,
// one line on what this screen is for, the screen, and its keys. The
// default listener keeps answering behind it, and the header says so.
func (m firstRunModel) wizardFrame() string {
	wz := m.wiz
	w := min(m.width-4, 100)
	live := stFaint.Render("guide at " + strings.TrimPrefix(m.url, "http://"))
	if m.visits > 0 {
		live = stPrimary.Render("✓ ") + live
	}
	left := stBrand.Render("hoop") + " " + stStrong.Render("sidecar") + "  " + stPrimary.Render(wz.crumbs())
	gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(live), 2)
	header := left + strings.Repeat(" ", gap) + live
	intro := lipgloss.NewStyle().Width(w).Inherit(stText).Render(wz.intro())
	hints := wz.hints()
	if m.stopping {
		hints = stStrong.Render("stopping…")
	}
	used := 1 + 1 + lipgloss.Height(intro) + 1 + 2 + 1 + 1
	body := pane("", wz.view(w-4, max(m.height-used-2, 4)), w, max(m.height-used, 6))
	page := lipgloss.JoinVertical(lipgloss.Left, header, "", intro, "", body, hints)
	if lines := strings.Split(page, "\n"); len(lines) > m.height {
		page = strings.Join(lines[:m.height], "\n")
	}
	page = ansiClamp(page, m.width)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, page)
}

// ansiClamp cuts every line of s to width cells, keeping its colors.
func ansiClamp(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(lines, "\n")
}
