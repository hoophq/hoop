package sidecartui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Every list carries a header row, and each label starts in the column its
// value starts in: a header that drifts from its rows names the wrong thing.
func TestListHeadersLineUpWithTheirRows(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 20, 0, time.UTC)
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	feed(m.st, script)
	m.width, m.height = 200, 40

	for _, tc := range []struct {
		tab          tab
		label, value string
	}{
		{tabWire, "LISTENER", "pg-prod"},
		{tabWire, "PRINCIPAL", "bob"},
		{tabSessions, "LISTENER", "pg-prod"},
		{tabSessions, "PRINCIPAL", "bob"},
		{tabReviews, "APPROVAL", "rv_1"},
		{tabReviews, "LISTENER", "pg-prod"},
		{tabLanes, "NAME", "pg-prod"},
		{tabLanes, "MODE", "ENFORCE"},
		{tabLogs, "LEVEL", "INFO"},
	} {
		m.tab = tc.tab
		keys := m.keys(tc.tab)
		lines := strings.Split(ansi.Strip(m.list(keys, 0, 180, 10)), "\n")
		if len(lines) < 2 {
			t.Fatalf("%s: no rows under the header", tabNames[tc.tab])
		}
		head, row := lines[0], lines[1]
		hi, vi := strings.Index(head, tc.label), strings.Index(row, tc.value)
		if hi < 0 || vi < 0 {
			t.Fatalf("%s: header %q or row %q is missing %q / %q", tabNames[tc.tab], head, row, tc.label, tc.value)
		}
		// Byte offsets after stripping are rune-safe here: every cell
		// before these columns is ASCII or the same glyphs on both lines.
		if ansi.StringWidth(head[:hi]) != ansi.StringWidth(row[:vi]) {
			t.Errorf("%s: %s starts at column %d, its value %q at %d\n%s\n%s", tabNames[tc.tab],
				tc.label, ansi.StringWidth(head[:hi]), tc.value, ansi.StringWidth(row[:vi]), head, row)
		}
	}
}
