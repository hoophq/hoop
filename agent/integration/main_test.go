//go:build integration

package integration

import (
	"os"
	"testing"

	"github.com/hoophq/hoop/agent/integration/testutil"
)

// TestMain owns the lifetime of the shared SQL Server. It is booted lazily
// by the first test that needs it, so teardown cannot live in t.Cleanup:
// that is what made the suite boot a container per test (ENG-511).
//
// m.Run's code is held in a variable because os.Exit must be the last call
// and it would otherwise skip the teardown.
func TestMain(m *testing.M) {
	code := m.Run()
	testutil.ShutdownSharedContainers()
	os.Exit(code)
}
