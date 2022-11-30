package styles

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var (
	color       = termenv.EnvColorProfile().Color
	Fainted     = lipgloss.NewStyle().Faint(true)
	Default     = lipgloss.NewStyle()
	ClientError = termenv.Style{}.
			Foreground(color("0")).
			Background(color("#DBAB79")).Styled
	Keyword = termenv.Style{}.
		Foreground(color("204")).
		Background(color("235")).Styled
)
