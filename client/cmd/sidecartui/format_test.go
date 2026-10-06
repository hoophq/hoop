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
		noStdin   bool
	}{
		{"terminal draws the tui", FormatAuto, true, nil, FormatTUI, false},
		{"pipe keeps json", FormatAuto, false, nil, FormatJSON, false},
		{"ci keeps json on a terminal", FormatAuto, true, map[string]string{"CI": "true"}, FormatJSON, false},
		{"no color writes text", FormatAuto, true, map[string]string{"NO_COLOR": "1"}, FormatText, false},
		{"dumb terminal writes text", FormatAuto, true, map[string]string{"TERM": "dumb"}, FormatText, false},
		{"no color on a pipe keeps json", FormatAuto, false, map[string]string{"NO_COLOR": "1"}, FormatJSON, false},
		{"no keyboard writes text", FormatAuto, true, nil, FormatText, true},
		{"flag wins over a pipe", FormatTUI, false, nil, FormatTUI, false},
		{"flag wins over ci", FormatTUI, true, map[string]string{"CI": "1"}, FormatTUI, false},
		{"flag wins over no keyboard", FormatTUI, true, nil, FormatTUI, true},
		{"flag wins over a terminal", FormatJSON, true, nil, FormatJSON, false},
		{"text flag on a terminal", FormatText, true, nil, FormatText, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.requested, tc.tty, !tc.noStdin, env(tc.env)); got != tc.want {
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

// Only a terminal with a keyboard takes approvals: a forced TUI on a pipe
// draws, but must keep refusing holds rather than hang every held client.
func TestInteractive(t *testing.T) {
	for _, tc := range []struct{ out, in, want bool }{
		{true, true, true}, {true, false, false}, {false, true, false}, {false, false, false},
	} {
		if got := Interactive(tc.out, tc.in); got != tc.want {
			t.Errorf("Interactive(stdout=%v, stdin=%v) = %v", tc.out, tc.in, got)
		}
	}
}

func TestDarkBackground(t *testing.T) {
	for in, want := range map[string]bool{"": true, "15;0": true, "0;15": false, "12;7": false, "15;default;0": true, "junk": true} {
		if got := darkBackground(in); got != want {
			t.Fatalf("darkBackground(%q) = %v, want %v", in, got, want)
		}
	}
}
