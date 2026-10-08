package sidecartui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The side menu shows every section whichever one is selected: the Bubbles
// list pages its items, and a page per section would hide the others.
func TestTheMenuShowsEverySection(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 20, 0, time.UTC)
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	feed(m.st, script)
	m.tour = newTour(&DemoOptions{})
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

// Try it belongs to the demo: a sidecar fronting anything else has no
// guide to offer, and its menu, tab keys and 7 must not reach one.
func TestTheTryItSectionIsOnlyThereForTheDemo(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 20, 0, time.UTC)
	m := newModel("dev", nil, func() time.Time { return now }, nil)
	m.width, m.height = 140, 40
	m.menuFocus = false
	if strings.Contains(ansi.Strip(m.render()), "Try it") {
		t.Error("the menu shows Try it without the demo")
	}
	var tm tea.Model = m
	tm, _ = tm.Update(key("7"))
	if tm.(model).tab == tabTour {
		t.Error("7 opened Try it without the demo")
	}
	m.tab = tabLogs
	tm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if tm.(model).tab != tabWire {
		t.Errorf("tab after Logs went to %s, want it to wrap to Wire", tabNames[tm.(model).tab])
	}
}
