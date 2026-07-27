package tunnelmgr

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

// TestConnRegistry_ConcurrentAccess is the regression guard for the
// data race this whole type exists to fix: the netstack accept/handler
// closures read the registry on every SYN while a refresh writes it.
// Run under `go test -race` it must report no races.
func TestConnRegistry_ConcurrentAccess(t *testing.T) {
	r := newConnRegistry()
	r.reconcile(map[string]string{"pg": "postgres", "my": "mysql"})

	stop := make(chan struct{})
	var readers sync.WaitGroup

	// Readers: mimic the accept func + listing/Snapshot paths. They run
	// until stop is closed.
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = r.subTypeOf("pg")
					_ = r.activeConnections()
				}
			}
		}()
	}

	// Writer: mimic the periodic refresh churning the active set. When
	// it finishes we stop the readers and wait for them to drain.
	for i := 0; i < 1000; i++ {
		r.reconcile(map[string]string{
			"pg":                       "postgres",
			fmt.Sprintf("dyn-%d", i%3): "mysql",
		})
	}
	close(stop)
	readers.Wait()
}

func TestConnRegistry_ReconcileAddsAndRetires(t *testing.T) {
	r := newConnRegistry()

	// First sync: two connections appear.
	added, removed := r.reconcile(map[string]string{
		"pg-prod": "postgres",
		"my-prod": "mysql",
	})
	if added != 2 || removed != 0 {
		t.Fatalf("first reconcile: added=%d removed=%d, want 2/0", added, removed)
	}
	if sub, ok := r.subTypeOf("pg-prod"); !ok || sub != "postgres" {
		t.Errorf("pg-prod subtype = %q ok=%v, want postgres/true", sub, ok)
	}

	// Second sync: my-prod vanished, a new mssql appeared, pg-prod stays.
	added, removed = r.reconcile(map[string]string{
		"pg-prod":  "postgres",
		"sql-prod": "mssql",
	})
	if added != 1 || removed != 1 {
		t.Fatalf("second reconcile: added=%d removed=%d, want 1/1", added, removed)
	}

	// my-prod is now inactive: subTypeOf must report not-found so the
	// accept path rejects new SYNs.
	if _, ok := r.subTypeOf("my-prod"); ok {
		t.Errorf("my-prod still active after it vanished from the gateway")
	}
	// The survivor and the newcomer are active.
	if _, ok := r.subTypeOf("pg-prod"); !ok {
		t.Errorf("pg-prod should still be active")
	}
	if _, ok := r.subTypeOf("sql-prod"); !ok {
		t.Errorf("sql-prod should be active")
	}

	got := r.activeConnections()
	names := make([]string, len(got))
	for i, c := range got {
		names[i] = c.Name
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "pg-prod" || names[1] != "sql-prod" {
		t.Errorf("activeConnections = %v, want [pg-prod sql-prod]", names)
	}
}

func TestConnRegistry_ReactivateKeepsNoStaleDuplicate(t *testing.T) {
	r := newConnRegistry()
	r.reconcile(map[string]string{"pg": "postgres"})
	// pg vanishes...
	r.reconcile(map[string]string{})
	if _, ok := r.subTypeOf("pg"); ok {
		t.Fatal("pg should be inactive after vanishing")
	}
	// ...then comes back. It must count as newly-added and be active
	// again with the same name (the allocator keeps its IP reserved).
	added, removed := r.reconcile(map[string]string{"pg": "postgres"})
	if added != 1 || removed != 0 {
		t.Fatalf("reactivate: added=%d removed=%d, want 1/0", added, removed)
	}
	if sub, ok := r.subTypeOf("pg"); !ok || sub != "postgres" {
		t.Errorf("pg subtype = %q ok=%v after reactivation, want postgres/true", sub, ok)
	}
	if n := len(r.activeConnections()); n != 1 {
		t.Errorf("activeConnections len = %d, want 1 (no stale duplicate)", n)
	}
}

func TestConnRegistry_IdempotentReconcile(t *testing.T) {
	r := newConnRegistry()
	r.reconcile(map[string]string{"pg": "postgres"})
	// Reconciling the identical set again must report no churn.
	added, removed := r.reconcile(map[string]string{"pg": "postgres"})
	if added != 0 || removed != 0 {
		t.Errorf("idempotent reconcile: added=%d removed=%d, want 0/0", added, removed)
	}
}
