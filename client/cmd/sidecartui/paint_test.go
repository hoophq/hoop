package sidecartui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// oneFrame draws view once through Bubble Tea's real renderer and returns
// the bytes it sent the terminal.
type oneFrame struct{ view string }

func (o oneFrame) Init() tea.Cmd { return nil }
func (o oneFrame) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return o, tea.Quit
	}
	return o, nil
}
func (o oneFrame) View() tea.View { return tea.NewView(o.view) }

func render(t *testing.T, view string) string {
	t.Helper()
	var out bytes.Buffer
	p := tea.NewProgram(oneFrame{view}, tea.WithOutput(&out), tea.WithInput(nil),
		tea.WithWindowSize(60, 3), tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}),
		tea.WithoutSignalHandler())
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

var ech = regexp.MustCompile(`\x1b\[\d*X`)

// TestPaintedPaddingIsNotErased pins why paintedBlank exists. Bubble Tea's
// renderer sends trailing blanks as ECH, which a terminal without
// background-color erase clears to its default, so a painted row's fill
// stopped at its text. If the first half fails, the renderer no longer
// does that: delete paintedBlank and this test.
func TestPaintedPaddingIsNotErased(t *testing.T) {
	bg := selectionSequence()
	if bg == "" {
		t.Skip("this renderer emits no background sequence")
	}
	plain := bg + "hi" + strings.Repeat(" ", 40) + "\x1b[m"
	if !ech.MatchString(render(t, plain)) {
		t.Fatal("Bubble Tea no longer erases trailing blanks with ECH; paintedBlank can go")
	}
	if got := render(t, withBackground("hi", 42)); ech.MatchString(got) {
		t.Errorf("a withBackground row is still sent with ECH, so its fill can be erased:\n%q", got)
	}
}
