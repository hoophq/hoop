package daemon

import (
	"os"
	"strings"
	"testing"
)

type mockRunner struct {
	runOut   string
	runErr   error
	logsErr  error
	callsRun []string
	callsLog []string
}

func (m *mockRunner) Run(name string, args ...string) (string, error) {
	m.callsRun = append(m.callsRun, name+" "+strings.Join(args, " "))
	return m.runOut, m.runErr
}

func (m *mockRunner) Logs(name string, args ...string) error {
	m.callsLog = append(m.callsLog, name+" "+strings.Join(args, " "))
	return m.logsErr
}

func withTempHome(t *testing.T) (restore func(), home string) {
	t.Helper()
	oldHome := os.Getenv("HOME")
	td := t.TempDir()
	if err := os.Setenv("HOME", td); err != nil {
		t.Fatalf("failed to set HOME to %q: %v", td, err)
	}

	return func() {
		_ = os.Setenv("HOME", oldHome)
	}, td
}


func requireContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("expected to contain %q, got:\n%s", substr, s)
	}
}
