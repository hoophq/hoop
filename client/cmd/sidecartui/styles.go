package sidecartui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// The palette matches the CLI's existing accents (client/cmd/styles: 204 for
// keywords, #DBAB79 for warnings) so the sidecar reads as the same product.
var (
	colAccent = lipgloss.Color("204")
	colWarn   = lipgloss.Color("#DBAB79")
	colOK     = lipgloss.Color("42")
	colBad    = lipgloss.Color("203")
	colInfo   = lipgloss.Color("75")
	colInk    = lipgloss.Color("0")
	colFaint  = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}
	colBorder = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
	colSelBg  = lipgloss.AdaptiveColor{Light: "254", Dark: "236"}

	stBrand  = lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(colAccent).Padding(0, 1)
	stFaint  = lipgloss.NewStyle().Foreground(colFaint)
	stBold   = lipgloss.NewStyle().Bold(true)
	stAccent = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stOK     = lipgloss.NewStyle().Foreground(colOK)
	stBad    = lipgloss.NewStyle().Foreground(colBad)
	stWarn   = lipgloss.NewStyle().Foreground(colWarn)
	stInfo   = lipgloss.NewStyle().Foreground(colInfo)
	stKey    = lipgloss.NewStyle().Foreground(colAccent)
	stLabel  = lipgloss.NewStyle().Foreground(colFaint)
	stTabOn  = lipgloss.NewStyle().Bold(true).Foreground(colAccent).Underline(true)
	stTabOff = lipgloss.NewStyle().Foreground(colFaint)
	stSel    = lipgloss.NewStyle().Background(colSelBg)
	stPane   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1)
	stTitle  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
)

func badge(text string, bg lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(bg).Padding(0, 1).Render(text)
}

// verdictBadge is the fixed-width tag that leads every wire row, so the eye
// can run down one column and find the denials.
func verdictBadge(kind string, allowed bool) string {
	switch kind {
	case "statement":
		return stOK.Render("✓ ALLOW ")
	case "violation":
		return stBad.Bold(true).Render("✕ DENY  ")
	case "masked":
		return stWarn.Render("▒ MASK  ")
	case "error":
		return stBad.Render("! ERROR ")
	case "activity":
		if !allowed {
			return stBad.Render("◆ REFUSE")
		}
		return stInfo.Render("◆ ACT   ")
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
		return stBad.Bold(true).Render("▲high")
	case "medium":
		return stWarn.Render("▲med")
	case "low":
		return stFaint.Render("▲low")
	}
	return ""
}

func reviewBadge(status string) string {
	switch status {
	case "PENDING":
		return badge("PENDING", colWarn)
	case "APPROVED":
		return badge("APPROVED", colOK)
	case "REJECTED", "REVOKED":
		return badge(status, colBad)
	}
	return lipgloss.NewStyle().Foreground(colFaint).Bold(true).Render(status)
}

func levelStyle(level string) lipgloss.Style {
	switch level {
	case "ERROR":
		return stBad.Bold(true)
	case "WARN":
		return stWarn.Bold(true)
	case "DEBUG":
		return stFaint
	}
	return stInfo
}

func protocolBadge(p string) string {
	if p == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(colInfo).Render(p)
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
			b.WriteString(stBad.Render(cell))
		} else {
			b.WriteString(stAccent.UnsetBold().Render(cell))
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
