package sidecartui

import (
	"fmt"
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

// The flow diagram's fixed parts, in cells: the three nodes and the spaces
// around both arrows. Each arrow is frSegment(width) dashes plus its head.
const (
	frNodeBrowser = len("o browser")
	frNodeSidecar = len("o sidecar")
	frNodeDocs    = len("o hoop.dev/docs")
	frFlowFixed   = frNodeBrowser + frNodeSidecar + frNodeDocs + 4 + 2
)

// frSegment is the length of each arrow: as long as 16, and shorter on a
// narrow terminal so the row never wraps.
func frSegment(width int) int {
	return max(min((width-4-frFlowFixed)/2, 16), 3)
}

// frFlow draws browser -> sidecar -> guide, with a dot crossing both arrows
// for every visit still in flight. It is the screen's answer to "did it
// work": the person opens the URL and watches the request go through.
func (m firstRunModel) frFlow(now time.Time) string {
	seg := frSegment(m.width)
	cells := make([]string, 2*seg)
	track := stFaint
	if m.visits > 0 {
		track = stKey
	}
	for i := range cells {
		cells[i] = track.Render("─")
	}
	dot := lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	for _, p := range m.pulses {
		f := float64(now.Sub(p)) / float64(pulseLife)
		if f < 0 || f >= 1 {
			continue
		}
		pos := int(f * float64(len(cells)))
		cells[pos] = dot.Render("●")
		if pos > 0 {
			cells[pos-1] = stKey.Render("•")
		}
	}
	arrow := func(c []string) string { return strings.Join(c, "") + track.Render("▶") }
	node := func(icon, label string, lit bool) string {
		st := stText
		if lit {
			st = stStrong
		}
		return stKey.Render(icon) + " " + st.Render(label)
	}
	row := node("◉", "browser", m.visits > 0) + " " + arrow(cells[:seg]) + " " +
		node("◆", "sidecar", true) + " " + arrow(cells[seg:]) + " " +
		node("◎", "hoop.dev/docs", m.visits > 0)

	addr := strings.TrimPrefix(m.url, "http://")
	if addr == "" {
		addr = "binding…"
	}
	// Under each element: what crosses the arrows, and where the sidecar
	// listens. Each label is centered on the element above it.
	arrow1 := frNodeBrowser + 1 + (seg+1)/2
	sidecar := frNodeBrowser + 1 + seg + 1 + 1 + frNodeSidecar/2
	arrow2 := sidecar + frNodeSidecar/2 + 1 + 1 + (seg+1)/2
	// As wide as the row above, or centering the two would shift one.
	under := []rune(strings.Repeat(" ", frFlowFixed+2*seg))
	// put writes label centered on center, unless it would run into a
	// label already there: the address wins, and an arrow's label is left
	// out on a terminal too narrow for both.
	put := func(center int, label string) {
		r := []rune(label)
		start := max(center-len(r)/2, 0)
		if start+len(r) > len(under) {
			return
		}
		for i := max(start-1, 0); i < min(start+len(r)+1, len(under)); i++ {
			if under[i] != ' ' {
				return
			}
		}
		copy(under[start:], r)
	}
	put(sidecar, addr)
	put(arrow1, "GET /")
	put(arrow2, "302 → guide")
	return row + "\n" + stFaint.Render(string(under))
}

// frStatus is one line: what the screen is waiting for, or that it happened.
func (m firstRunModel) frStatus(now time.Time) string {
	switch {
	case !m.ready:
		return shimmer("binding a loopback port…", now)
	case m.visits == 0:
		return shimmer("waiting for your browser…", now) + stFaint.Render("  open the URL below")
	}
	ago := short(now.Sub(m.lastVisit))
	return stPrimary.Render("✓ opened") + stFaint.Render(fmt.Sprintf("  ·  %d visit%s  ·  last %s ago",
		m.visits, plural(m.visits), ago))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// frSteps is the getting-started path, each step ticking off as it happens.
func (m firstRunModel) frSteps(now time.Time) string {
	mark := func(n int, done bool) string {
		if done {
			return stPrimary.Render(" ✓ ")
		}
		return badge(fmt.Sprint(n), colPrimary)
	}
	url := m.url
	if url == "" {
		url = "…"
	}
	step2 := stText.Render("Press ") + stKey.Render("w") + stText.Render(" to set up a config: ") +
		stFaint.Render("the demo, or your own database or API")
	if m.detecting {
		step2 = shimmer("looking at this machine…", now)
	}
	if m.saved != "" {
		step2 = stText.Render("Saved ") + stStrong.Render(m.saved) + stFaint.Render("  w to set up another")
	}
	step3 := stText.Render("Save and boot it, or run ") + stStrong.Render("hoop start sidecar --config <file>")
	if m.saved != "" {
		step3 = stText.Render("Run ") + stStrong.Render("hoop start sidecar --config "+m.saved)
	}
	lines := []string{
		mark(1, m.visits > 0) + "  " + stText.Render("Open ") + stPrimary.Underline(true).Render(url) +
			stFaint.Render("  the getting-started guide"),
		mark(2, m.saved != "") + "  " + step2,
		mark(3, false) + "  " + step3,
	}
	return strings.Join(lines, "\n")
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
	if m.width >= 70 && m.height >= 28 {
		head = frWordmark(now) + "\n" + stFaint.Render("the inspection sidecar · "+m.version)
	} else {
		head = stBrand.Render("hoop") + " " + stStrong.Render("sidecar") + stFaint.Render("  "+m.version)
	}

	// Wrapped to the frame, so a narrow terminal breaks the sentence
	// instead of cutting it.
	wrap := lipgloss.NewStyle().Width(w).Align(lipgloss.Center)
	intro := wrap.Inherit(stText).Render("No config given, so a built-in default is running. It inspects no traffic.")
	if m.fellBack {
		intro += "\n" + wrap.Inherit(stFaint).Render("Port 15321 was busy; a free port was bound instead.")
	}

	body := pane(stTitle.Render("Get started"), m.frSteps(now), w, 6)

	hints := stKey.Render("w") + stFaint.Render(" set up a config   ") + stKey.Render("q") + stFaint.Render(" quit")
	if m.stopping {
		hints = stStrong.Render("stopping…") + stFaint.Render("  q again to leave now")
	}

	blocks := []string{head, "", intro, "", m.frFlow(now), "", m.frStatus(now), "", body, "", hints}
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
