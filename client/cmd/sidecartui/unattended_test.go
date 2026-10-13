package sidecartui

import "testing"

// A coding agent or a deployment holds a terminal nobody is at; a person's
// shell, with any of the usual variables, does not.
func TestUnattended(t *testing.T) {
	cases := []struct {
		name  string
		env   map[string]string
		files []string
		want  bool
	}{
		{"a person's terminal", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "vscode"}, nil, false},
		{"claude code", map[string]string{"CLAUDECODE": "1"}, nil, true},
		{"generic agent marker", map[string]string{"AI_AGENT": "some-agent"}, nil, true},
		{"cursor agent", map[string]string{"CURSOR_AGENT": "1"}, nil, true},
		{"gemini cli", map[string]string{"GEMINI_CLI": "1"}, nil, true},
		{"codex", map[string]string{"CODEX_SANDBOX": "seatbelt"}, nil, true},
		{"kubernetes pod", map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"}, nil, true},
		{"podman or nspawn", map[string]string{"container": "podman"}, nil, true},
		{"docker", nil, []string{"/.dockerenv"}, true},
		{"podman file", nil, []string{"/run/.containerenv"}, true},
	}
	for _, c := range cases {
		getenv := func(k string) string { return c.env[k] }
		exists := func(p string) bool {
			for _, f := range c.files {
				if f == p {
					return true
				}
			}
			return false
		}
		if got := Unattended(getenv, exists) != ""; got != c.want {
			t.Errorf("%s: unattended = %v, want %v", c.name, got, c.want)
		}
	}
}
