//go:build integration

package testutil

import (
	"runtime"
	"time"
)

// GoroutineLeakChecker captures the goroutine count at construction time
// and asserts at test end that the count returned within a tolerance.
//
// This is a coarse signal — it can't tell which goroutines leaked, only
// that the total grew. False positives are possible because testcontainers,
// the Docker SDK, and the Go runtime spawn background goroutines that
// stick around between sub-tests. The tolerance absorbs that noise.
//
// Use it like:
//
//	leak := testutil.SnapshotGoroutines(t, 10)
//	// ... run test ...
//	leak.Assert(t)
type GoroutineLeakChecker struct {
	startCount int
	tolerance  int
}

// SnapshotGoroutines captures the current goroutine count. Call Assert on
// the returned checker after teardown to verify no significant leak.
//
// The tolerance is the maximum allowed delta. A value of 0 means strict
// (no growth allowed); typical values for integration tests are 5–20
// to absorb testcontainer / docker-client noise.
//
// The function settles briefly before capturing the baseline so that
// goroutines from a just-completed setup step have time to exit.
func SnapshotGoroutines(t T, tolerance int) *GoroutineLeakChecker {
	t.Helper()
	settle()
	return &GoroutineLeakChecker{
		startCount: runtime.NumGoroutine(),
		tolerance:  tolerance,
	}
}

// Assert checks that the goroutine count has not grown beyond tolerance.
// It settles briefly first to allow in-flight teardown goroutines to exit.
// On failure, it logs the start count, current count, and the goroutine
// dump so the developer can see what's still running.
func (g *GoroutineLeakChecker) Assert(t T) {
	t.Helper()
	settle()

	end := runtime.NumGoroutine()
	delta := end - g.startCount
	if delta > g.tolerance {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutine leak: started with %d, ended with %d (delta=%d, tolerance=%d)\n\n=== goroutine dump ===\n%s",
			g.startCount, end, delta, g.tolerance, buf[:n])
	}
}

// settle gives runtime goroutines a chance to schedule out before measuring.
// 100ms is overkill for normal cleanup but cheap relative to test latency.
func settle() {
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
}
