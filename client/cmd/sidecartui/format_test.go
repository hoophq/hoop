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
		// An AI coding agent in a pseudo-terminal cannot drive a dashboard.
		{"claude code on a terminal writes text", FormatAuto, true, map[string]string{"CLAUDECODE": "1"}, FormatText, false},
		{"cursor on a terminal writes text", FormatAuto, true, map[string]string{"CURSOR_AGENT": "1"}, FormatText, false},
		{"codex on a terminal writes text", FormatAuto, true, map[string]string{"CODEX_SANDBOX": "seatbelt"}, FormatText, false},
		{"gemini on a terminal writes text", FormatAuto, true, map[string]string{"GEMINI_CLI": "1"}, FormatText, false},
		{"an agent on a pipe keeps json", FormatAuto, false, map[string]string{"CLAUDECODE": "1"}, FormatJSON, false},
		{"flag wins over an agent", FormatTUI, true, map[string]string{"CLAUDECODE": "1"}, FormatTUI, false},
		// A pod or a task with terminals allocated still ships its logs.
		{"kubernetes with tty and stdin keeps json", FormatAuto, true, map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"}, FormatJSON, false},
		{"ecs with a tty keeps json", FormatAuto, true, map[string]string{"ECS_CONTAINER_METADATA_URI_V4": "http://169.254.170.2/v4/x"}, FormatJSON, false},
		{"flag wins in a pod", FormatTUI, true, map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"}, FormatTUI, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.requested, tc.tty, !tc.noStdin, env(tc.env), noFiles); got != tc.want {
				t.Fatalf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

func noFiles(string) bool { return false }

// A container given a terminal (`docker run -it`, compose with tty and
// stdin_open) still ships its logs, though plain Docker sets no variable.
func TestResolveInAContainer(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		file string
	}{
		{"docker run -it", nil, "/.dockerenv"},
		{"podman run -it", nil, "/run/.containerenv"},
		{"podman, nspawn or lxc variable", map[string]string{"container": "podman"}, ""},
		{"nomad allocation", map[string]string{"NOMAD_ALLOC_ID": "abc"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exists := func(p string) bool { return p == tc.file }
			if got := Resolve(FormatAuto, true, true, env(tc.env), exists); got != FormatJSON {
				t.Fatalf("Resolve = %q, want json", got)
			}
			if got := Resolve(FormatTUI, true, true, env(tc.env), exists); got != FormatTUI {
				t.Fatalf("an explicit tui = %q, want it kept", got)
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
