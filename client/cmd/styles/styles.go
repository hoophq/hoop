package styles

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var (
	Fainted = lipgloss.NewStyle().Faint(true)
	Default = lipgloss.NewStyle()
	color   = termenv.EnvColorProfile().Color
	Keyword = termenv.Style{}.
		Foreground(color("204")).
		Background(color("235")).Styled
)
