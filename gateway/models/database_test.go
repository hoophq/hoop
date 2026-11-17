package models_test

import (
	"sync"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
)

func TestInitDatabaseConnectionRefusesUnboundedPool(t *testing.T) {
	for _, n := range []int{0, -1} {
		if err := models.InitDatabaseConnection("postgres://u:p@localhost:5432/db", n); err == nil {
			t.Fatalf("InitDatabaseConnection(cap=%d) = nil, want error", n)
		}
	}
}

// Queries past the cap wait for a free connection instead of opening one:
// the server never sees more sessions than the cap, whatever the request
// concurrency. The embedded backend is single-session, so the cap is 1 and
// any second connection would fail the queries, not only the assertion.
func TestInitDatabaseConnectionCapsOpenConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatalf("open pool: %v", err)
	}
	sqlDB, err := models.DB.DB()
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	const workers, perWorker = 20, 5
	errs := make(chan error, workers*perWorker)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perWorker {
				var one int
				errs <- models.DB.Raw("SELECT 1").Scan(&one).Error
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("query failed under the cap: %v", err)
		}
	}
	st := sqlDB.Stats()
	if st.MaxOpenConnections != 1 || st.OpenConnections > 1 {
		t.Fatalf("pool open=%d max=%d, want at most 1", st.OpenConnections, st.MaxOpenConnections)
	}
	if st.WaitCount == 0 {
		t.Fatal("expected queries past the cap to wait for a connection")
	}
}
