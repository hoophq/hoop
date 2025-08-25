package daemon

import (
	"os"
	"strings"
	"testing"
)

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
