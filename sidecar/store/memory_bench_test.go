package store_test

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/hoophq/hoop/sidecar/store"
)

var benchStatement = "SELECT id, name, email, created_at FROM customers WHERE tenant_id = 42 AND " +
	strings.Repeat("status = 'active' AND ", 8) + "deleted_at IS NULL ORDER BY created_at DESC LIMIT 100"

func benchEvent(id session.ID) audit.Event {
	return audit.Event{
		Kind: audit.KindStatement, Timestamp: time.Now().UTC(), SessionID: id,
		Principal: "svc-pool@example.com", Protocol: inspect.Postgres, Connection: "appdb",
		Operation: inspect.OpSelect, Statement: benchStatement, Tables: []string{"customers"},
		Allowed: true, Metadata: map[string]string{"pg.message": "Q", "lane": "appdb"},
	}
}

func newBenchStore() *store.MemoryStore { return store.NewMemoryStore(store.MemoryOptions{MaxSessions: 1000}) }

// BenchmarkWriteLongLivedSessions is the ENG-623 load: a pool of 16 sessions
// that never close, writing statements forever.
func BenchmarkWriteLongLivedSessions(b *testing.B) {
	s := newBenchStore()
	ids := make([]session.ID, 16)
	for i := range ids {
		ids[i] = session.ID("pool-" + strconv.Itoa(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_ = s.Write(ctx(), benchEvent(ids[i%len(ids)]))
	}
	b.StopTimer()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	b.ReportMetric(float64(ms.HeapInuse)/(1<<20), "heap-MiB")
	b.ReportMetric(float64(ms.HeapAlloc)/(1<<20), "live-MiB")
	runtime.KeepAlive(s)
}

// BenchmarkWriteSessionChurn keeps the store full of sessions, so every new
// session evicts one.
func BenchmarkWriteSessionChurn(b *testing.B) {
	s := newBenchStore()
	const perSession = 50
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_ = s.Write(ctx(), benchEvent(session.ID("s-"+strconv.Itoa(i/perSession))))
	}
}
