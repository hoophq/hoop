//go:build integration

package integration

import (
	"os"
	"testing"

	"github.com/hoophq/hoop/agent/integration/testutil"
)

// TestMain owns the lifetime of the shared upstream containers. They are
// booted lazily by the first test that needs one and must be terminated
// here, because the per-test t.Cleanup that used to do it is exactly what
// made the suite boot a container per test (ENG-511).
//
// The deferred-cleanup shape is deliberate: os.Exit skips deferred calls,
// so m.Run's code is captured and the exit happens after teardown. Leaving
// containers behind would strand them until testcontainers' reaper
// collects them, well after the job has moved on.
func TestMain(m *testing.M) {
	code := m.Run()
	testutil.ShutdownSharedContainers()
	os.Exit(code)
}
