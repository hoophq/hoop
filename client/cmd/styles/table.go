package styles

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

var (
	tableHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#DBAB79")).
				Bold(true).
				Padding(0, 1)

	tableCellStyle = lipgloss.NewStyle().Padding(0, 1)

	tableBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
)

// RenderTable renders a styled table with a colored header row and a thin
// separator line. No outer borders or column dividers are drawn, keeping
// the output clean for terminal lists.
func RenderTable(headers []string, rows [][]string) string {
	t := table.New().
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderHeader(true).
		BorderStyle(tableBorderStyle).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return tableHeaderStyle
			}
			return tableCellStyle
		}).
		Headers(headers...).
		Rows(rows...)

	return t.Render()
}
