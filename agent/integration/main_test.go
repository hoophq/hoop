//go:build integration

package integration

import (
	"os"
	"testing"

	"github.com/hoophq/hoop/agent/integration/testutil"
)

// TestMain owns the shared SQL Server's lifetime. Teardown cannot live in
// t.Cleanup: that is what made the suite boot a container per test (ENG-511).
func TestMain(m *testing.M) {
	// Held in a variable because os.Exit would skip the teardown below.
	code := m.Run()
	testutil.ShutdownSharedContainers()
	os.Exit(code)
}
