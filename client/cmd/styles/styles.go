package styles

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var (
	color             = termenv.EnvColorProfile().Color
	Default           = lipgloss.NewStyle()
	ClientErrorSimple = termenv.Style{}.Foreground(color("#DBAB79")).Styled
	ClientError       = termenv.Style{}.
				Foreground(color("0")).
				Background(color("#DBAB79")).Styled
	Keyword = termenv.Style{}.
		Foreground(color("204")).
		Background(color("235")).Styled

	KeywordHighlight = termenv.Style{}.Foreground(color("204")).Styled
)

func PrintErrorAndExit(format string, v ...any) {
	errOutput := ClientError(fmt.Sprintf(format, v...))
	fmt.Println(errOutput)
	os.Exit(1)
}

func Fainted(format string, a ...any) string {
	return lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf(format, a...))
}
