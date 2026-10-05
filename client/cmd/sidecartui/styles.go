package sidecartui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// The palette is Hoop's: grayscale for everything that is only information,
// and two colors for what an operator acts on. Blue (#3E63DD) is the primary
// color: the brand, the active tab, keys, live state and reviews. Red
// (#C4060A) means refused or broken: denials, errors, high risk. A color
// anywhere else would dilute those two, so allowed traffic stays gray.
var (
	colPrimary = lipgloss.Color("#3E63DD")
	colDanger  = lipgloss.Color("#C4060A")
	// colInk is text on a blue or red background.
	colInk    = lipgloss.Color("#FFFFFF")
	colFaint  = lipgloss.AdaptiveColor{Light: "#8B8B8B", Dark: "#7A7A7A"}
	colStrong = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#EDEDED"}
	colBorder = lipgloss.AdaptiveColor{Light: "#D4D4D4", Dark: "#3A3A3A"}
	colSelBg  = lipgloss.AdaptiveColor{Light: "#EBEBEB", Dark: "#2B2B2B"}
	// colNeutral fills a button that is safe and not an action: the No of
	// a "stop the sidecar?" prompt. Gray, so blue keeps meaning "act".
	colNeutral = lipgloss.AdaptiveColor{Light: "#D4D4D4", Dark: "#4A4A4A"}

	stBrand   = lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(colPrimary).Padding(0, 1)
	stFaint   = lipgloss.NewStyle().Foreground(colFaint)
	stBold    = lipgloss.NewStyle().Bold(true)
	stText    = lipgloss.NewStyle().Foreground(colStrong)
	stStrong  = lipgloss.NewStyle().Foreground(colStrong).Bold(true)
	stPrimary = lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	stDanger  = lipgloss.NewStyle().Foreground(colDanger)
	stKey     = lipgloss.NewStyle().Foreground(colPrimary)
	stLabel   = lipgloss.NewStyle().Foreground(colFaint)
	stTabOn   = lipgloss.NewStyle().Bold(true).Foreground(colPrimary).Underline(true)
	stTabOff  = lipgloss.NewStyle().Foreground(colFaint)
	stSel     = lipgloss.NewStyle().Background(colSelBg)
	stPane    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1)
	stTitle   = stStrong
)

func badge(text string, bg lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(bg).Padding(0, 1).Render(text)
}

// verdictBadge is the fixed-width tag that leads every wire row, so the eye
// can run down one column and find the denials: the only red in it.
func verdictBadge(kind string, allowed bool) string {
	switch kind {
	case "statement":
		return stFaint.Render("✓ ALLOW ")
	case "violation":
		return stDanger.Bold(true).Render("✕ DENY  ")
	case "masked":
		return stStrong.Render("▒ MASK  ")
	case "error":
		return stDanger.Render("! ERROR ")
	case "activity":
		if !allowed {
			return stDanger.Render("◆ REFUSE")
		}
		return stText.Render("◆ ACT   ")
	case "session_start":
		return stFaint.Render("→ OPEN  ")
	case "session_end":
		return stFaint.Render("← CLOSE ")
	}
	return stFaint.Render(fmt.Sprintf("%-8s", kind))
}

func riskBadge(level string) string {
	switch level {
	case "high":
		return stDanger.Bold(true).Render("▲high")
	case "medium":
		return stStrong.Render("▲med")
	case "low":
		return stFaint.Render("▲low")
	}
	return ""
}

// reviewBadge: a pending review is the call to action, so it is blue text;
// an approval is the confirmed state, solid blue; a refusal is solid red.
func reviewBadge(status string) string {
	switch status {
	case "PENDING":
		return stPrimary.Padding(0, 1).Render("PENDING")
	case "APPROVED":
		return badge("APPROVED", colPrimary)
	case "REJECTED", "REVOKED":
		return badge(status, colDanger)
	}
	return stFaint.Bold(true).Render(status)
}

func levelStyle(level string) lipgloss.Style {
	switch level {
	case "ERROR":
		return stDanger.Bold(true)
	case "WARN":
		return stStrong
	case "DEBUG":
		return stFaint
	}
	return stText
}

func protocolBadge(p string) string {
	if p == "" {
		return ""
	}
	return stFaint.Render(p)
}

// shimmer draws text in the primary blue with a bright band sweeping across
// it, left to right, about once a second and a half. It asks for attention
// without taking the screen: the eye catches the movement, and nothing opens
// until the person goes there.
//
// The frame is a function of the clock, so it moves on every redraw the
// spinner already causes; no timer of its own. The band's head is white on
// blue, readable on a dark or a light terminal alike.
func shimmer(text string, now time.Time) string {
	runes := []rune(text)
	period := len(runes) + 8
	head := int(now.UnixMilli()/70) % period
	var b strings.Builder
	for i, r := range runes {
		cell := string(r)
		switch d := head - i; {
		case d == 0:
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(colPrimary).Render(cell))
		case d == 1 || d == 2:
			b.WriteString(stPrimary.Render(cell))
		default:
			b.WriteString(stKey.Render(cell))
		}
	}
	return b.String()
}

var sparkBlocks = []rune(" ▁▂▃▄▅▆▇█")

// sparkline draws one cell per second. A second with a denial is drawn red,
// so a burst of refusals stands out of a busy minute.
func sparkline(stmts, denied []int) string {
	top := 1
	for _, v := range stmts {
		top = max(top, v)
	}
	var b strings.Builder
	for i, v := range stmts {
		r := sparkBlocks[0]
		if v > 0 {
			r = sparkBlocks[1+(v*(len(sparkBlocks)-2))/top]
		}
		cell := string(r)
		if denied[i] > 0 {
			b.WriteString(stDanger.Render(cell))
		} else {
			b.WriteString(stKey.Render(cell))
		}
	}
	return b.String()
}

// num writes 12345 as 12,345.
func num(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 || len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// short renders a duration the way an operator says it: 4s, 3m12s, 2h05m.
func short(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
}

// oneLine collapses a statement onto one row.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
