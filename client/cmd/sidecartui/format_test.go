package sidecartui

import "testing"

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested Format
		tty       bool
		env       map[string]string
		want      Format
	}{
		{"terminal draws the tui", FormatAuto, true, nil, FormatTUI},
		{"pipe keeps json", FormatAuto, false, nil, FormatJSON},
		{"ci keeps json on a terminal", FormatAuto, true, map[string]string{"CI": "true"}, FormatJSON},
		{"no color writes text", FormatAuto, true, map[string]string{"NO_COLOR": "1"}, FormatText},
		{"dumb terminal writes text", FormatAuto, true, map[string]string{"TERM": "dumb"}, FormatText},
		{"no color on a pipe keeps json", FormatAuto, false, map[string]string{"NO_COLOR": "1"}, FormatJSON},
		{"flag wins over a pipe", FormatTUI, false, nil, FormatTUI},
		{"flag wins over ci", FormatTUI, true, map[string]string{"CI": "1"}, FormatTUI},
		{"flag wins over a terminal", FormatJSON, true, nil, FormatJSON},
		{"text flag on a terminal", FormatText, true, nil, FormatText},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.requested, tc.tty, env(tc.env)); got != tc.want {
				t.Fatalf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"": FormatAuto, "TUI": FormatTUI, " json ": FormatJSON, "text": FormatText} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Fatalf("ParseFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseFormat("pretty"); err == nil {
		t.Fatal("ParseFormat(pretty) accepted an unknown format")
	}
}

func TestDarkBackground(t *testing.T) {
	for in, want := range map[string]bool{"": true, "15;0": true, "0;15": false, "12;7": false, "15;default;0": true, "junk": true} {
		if got := darkBackground(in); got != want {
			t.Fatalf("darkBackground(%q) = %v, want %v", in, got, want)
		}
	}
}
