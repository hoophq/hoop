package sidecartui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// The side menu shows every section whichever one is selected: the Bubbles
// list pages its items, and a page per section would hide the others.
func TestTheMenuShowsEverySection(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 20, 0, time.UTC)
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	feed(m.st, script)
	for _, size := range [][2]int{{60, 14}, {140, 40}} {
		m.width, m.height = size[0], size[1]
		for tb := range tabCount {
			m.tab = tb
			screen := ansi.Strip(m.render())
			for i, name := range tabNames {
				if !strings.Contains(screen, fmt.Sprintf("%d %s", i+1, name)) {
					t.Errorf("%dx%d on %s: the menu does not show %s", size[0], size[1], tabNames[tb], name)
				}
			}
		}
	}
}
