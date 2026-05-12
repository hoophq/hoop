//go:build integration

package testutil

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/lib/pq"
)

// PGBackendCount opens a sidecar admin connection to the Postgres container
// and returns the number of currently-active backend processes for the
// given database, *excluding* the sidecar connection itself.
//
// Used by concurrency tests to assert "exactly one connection landed at
// the upstream" — the sidecar's own connection is filtered out via
// pg_backend_pid().
//
// Each call opens and closes its own admin connection so the count
// snapshot is taken from a fresh session. Don't call this in a tight
// loop; once-per-assertion is the intended use.
func PGBackendCount(t T, pg *PGContainer) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sql.Open("postgres", pg.ConnString())
	if err != nil {
		t.Fatalf("pgstat: failed to open admin connection: %v", err)
	}
	defer db.Close()

	// Sanity: ensure the connection works before we count, otherwise a
	// transient failure shows up as "0 backends" which is misleading.
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("pgstat: admin ping failed: %v", err)
	}

	var count int
	row := db.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`, pg.Database)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("pgstat: failed to count backends: %v", err)
	}
	return count
}

// WaitForPGBackendCount polls PGBackendCount until it equals want or the
// timeout elapses. Useful for waiting out connection teardown (Postgres
// reaps idle backends, but not instantly).
func WaitForPGBackendCount(t T, pg *PGContainer, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int
	for time.Now().Before(deadline) {
		last = PGBackendCount(t, pg)
		if last == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pgstat: expected %d backends after %v, last observed=%d", want, timeout, last)
}


