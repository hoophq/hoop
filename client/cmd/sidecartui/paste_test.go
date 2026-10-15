package sidecartui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// paste sends text the way a terminal with bracketed paste does: one
// tea.PasteMsg, never key presses.
func paste(t *testing.T, m firstRunModel, text string) firstRunModel {
	t.Helper()
	tm, _ := m.Update(tea.PasteMsg{Content: text})
	return tm.(firstRunModel)
}

// The Connect page takes a pasted URL, token and license, each as one line:
// a value copied from a terminal or a web page often ends in a newline.
func TestTheConnectPageTakesAPaste(t *testing.T) {
	m := openConnect(t, homeModel(t, t.TempDir(), validateFile))
	for _, c := range []struct{ id, pasted, want string }{
		{"url", "https://cp.example.com\n", "https://cp.example.com"},
		{"token", "  hsc_abc123\r\n", "hsc_abc123"},
	} {
		focusOn(m, c.id)
		m = paste(t, m, c.pasted)
		if got := m.connect.form.byID(c.id).text; got != c.want {
			t.Errorf("%s after a paste = %q, want %q", c.id, got, c.want)
		}
	}
	choose(m, unlockLicense)
	focusOn(m, "license")
	m = paste(t, m, "{\"payload\":\n{\"type\":\"enterprise\"}}\n")
	if got, want := m.connect.form.byID("license").text, `{"payload":{"type":"enterprise"}}`; got != want {
		t.Errorf("license after a paste = %q, want %q", got, want)
	}
}

// A paste lands in the field, not on a button: with the cursor on Talk to us
// it changes nothing.
func TestAPasteOnAButtonChangesNothing(t *testing.T) {
	m := openConnect(t, homeModel(t, t.TempDir(), validateFile))
	m = paste(t, m, "https://cp.example.com")
	for _, id := range []string{"url", "token", "license"} {
		if got := m.connect.form.byID(id).text; got != "" {
			t.Errorf("%s = %q after a paste on Talk to us", id, got)
		}
	}
}

// A number field takes a pasted number and drops anything else, as it drops
// a typed letter.
func TestANumberFieldTakesOnlyAPastedNumber(t *testing.T) {
	f := newForm("t", &field{id: "port", label: "Port", kind: fInt})
	f.paste(tea.PasteMsg{Content: "5432\n"})
	if got := f.byID("port").text; got != "5432" {
		t.Errorf("port after a paste = %q, want 5432", got)
	}
	f.paste(tea.PasteMsg{Content: "abc"})
	if got := f.byID("port").text; got != "5432" {
		t.Errorf("port after pasting letters = %q, want it unchanged", got)
	}
}
