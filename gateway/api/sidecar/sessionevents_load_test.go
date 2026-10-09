package apisidecar

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A load test of POST /api/sidecars/events: several sidecars, each sending
// its sessions' statements in batches the way the sink does (500 events, one
// sender per sidecar). It reports batch latency by how large the sessions'
// streams have grown, and fails when the last fifth is over twice the first.
//
// Skipped unless HOOP_SIDECAR_LOAD_PG names an EMPTY Postgres database; the
// test migrates it and refuses one that holds an organization.
//
//	HOOP_SIDECAR_LOAD_SIDECARS    default 10
//	HOOP_SIDECAR_LOAD_SESSIONS    sessions per sidecar, default 20
//	HOOP_SIDECAR_LOAD_STATEMENTS  statements per session, default 5000
//	HOOP_SIDECAR_LOAD_BYTES       bytes per statement, default 200
//
//	HOOP_SIDECAR_LOAD_PG=postgres://postgres@127.0.0.1:5432/hoop_load?sslmode=disable \
//	  go test ./gateway/api/sidecar/ -run TestLoadSessionEvents -count=1 -v -timeout 60m
func TestLoadSessionEvents(t *testing.T) {
	dsn := os.Getenv("HOOP_SIDECAR_LOAD_PG")
	if dsn == "" {
		t.Skip("set HOOP_SIDECAR_LOAD_PG to an empty Postgres database to run the load test")
	}
	sidecars := loadEnvInt(t, "HOOP_SIDECAR_LOAD_SIDECARS", 10)
	sessions := loadEnvInt(t, "HOOP_SIDECAR_LOAD_SESSIONS", 20)
	statements := loadEnvInt(t, "HOOP_SIDECAR_LOAD_STATEMENTS", 5000)
	stmtBytes := loadEnvInt(t, "HOOP_SIDECAR_LOAD_BYTES", 200)

	require.NoError(t, modelsbootstrap.MigrateDB(dsn, ""))
	require.NoError(t, models.InitDatabaseConnection(dsn, sidecars+5))
	var orgs int64
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.orgs`).Scan(&orgs).Error)
	require.Zero(t, orgs, "HOOP_SIDECAR_LOAD_PG must be an empty database; it holds %d organizations", orgs)
	orgID := uuid.NewString()
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'sidecar-load')`, orgID).Error)
	enableSessionEvents(t, orgID)

	var (
		mu      sync.Mutex
		samples []loadSample
		wg      sync.WaitGroup
		start   = time.Now()
	)
	for i := range sidecars {
		sc := seedEventsSidecar(t, orgID, fmt.Sprintf("load-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := sendLoad(t, sc, sessions, statements, stmtBytes)
			mu.Lock()
			samples = append(samples, got...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	events := sidecars * sessions * (statements + 2)
	t.Logf("%d sidecars x %d sessions x %d statements of %d bytes: %d events in %s, %.0f events/s, %d batches",
		sidecars, sessions, statements, stmtBytes, events, elapsed.Round(time.Millisecond),
		float64(events)/elapsed.Seconds(), len(samples))
	p50 := reportLoad(t, samples, statements)
	// The floor keeps scheduler noise on fast batches from failing the test.
	first, last := p50[0], p50[len(p50)-1]
	assert.LessOrEqual(t, last, max(2*first, 25*time.Millisecond),
		"a batch at the end of a session takes %s, at its start %s: the cost grows with the session", last, first)

	var stream struct {
		Disk int64
		Text int64
	}
	require.NoError(t, models.DB.Raw(`SELECT avg(pg_column_size(b.blob_stream) + coalesce(c.disk, 0))::bigint AS disk,
		avg(octet_length(b.blob_stream::text) + coalesce(c.text, 0))::bigint AS text
		FROM private.blobs b JOIN private.sessions s ON s.blob_stream_id = b.id
		LEFT JOIN (SELECT blob_id, sum(pg_column_size(entries)) AS disk, sum(octet_length(entries::text)) AS text
			FROM private.session_stream_chunks GROUP BY blob_id) c ON c.blob_id = b.id
		WHERE s.org_id = ?`, orgID).Scan(&stream).Error)
	t.Logf("stream per session: %d KiB as text, %d KiB on disk", stream.Text>>10, stream.Disk>>10)

	var open int64
	require.NoError(t, models.DB.Raw(`SELECT count(*) FROM private.sessions WHERE org_id = ? AND status <> 'done'`,
		orgID).Scan(&open).Error)
	require.Zero(t, open, "every session ended")
}

// loadSample is one batch: how long it took, and how many statements each of
// its sessions held before it.
type loadSample struct {
	Took  time.Duration
	Depth int
}

// sendLoad runs one sidecar: its sessions advance together, one batch of up to
// MaxSessionEventsBatch events at a time, as the sink drains its queue.
func sendLoad(t *testing.T, sc *models.Sidecar, sessions, statements, stmtBytes int) []loadSample {
	var queue []daemon.SessionEvent
	for s := range sessions {
		sid := fmt.Sprintf("%s-%d", sc.Name, s)
		queue = append(queue, sessionEvent(1, sid, 0, audit.KindSessionStart, nil))
	}
	for n := range statements {
		for s := range sessions {
			sid := fmt.Sprintf("%s-%d", sc.Name, s)
			queue = append(queue, sessionEvent(int64(n+2), sid, n/100, audit.KindStatement, func(e *audit.Event) {
				e.Statement = loadStatement(stmtBytes)
			}))
		}
	}
	for s := range sessions {
		sid := fmt.Sprintf("%s-%d", sc.Name, s)
		queue = append(queue, sessionEvent(int64(statements+2), sid, statements/100+1, audit.KindSessionEnd, nil))
	}

	var out []loadSample
	for len(queue) > 0 {
		n := min(daemon.MaxSessionEventsBatch, len(queue))
		batch := queue[:n]
		queue = queue[n:]
		body := eventsBody(t, batch...)
		began := time.Now()
		rec := postEvents(sc, body)
		took := time.Since(began)
		if rec.Code != http.StatusOK {
			t.Errorf("sidecar %s: batch answered %d: %s", sc.Name, rec.Code, rec.Body.String())
			return out
		}
		out = append(out, loadSample{Took: took, Depth: int(batch[0].Seq)})
	}
	return out
}

// reportLoad logs latency percentiles per fifth of the session depth and
// returns the p50 of each fifth. A flat table means a batch costs the same at
// the end of a session as at its start.
func reportLoad(t *testing.T, samples []loadSample, statements int) []time.Duration {
	const buckets = 5
	byBucket := make([][]time.Duration, buckets)
	for _, s := range samples {
		b := min(s.Depth*buckets/(statements+2), buckets-1)
		byBucket[b] = append(byBucket[b], s.Took)
	}
	t.Logf("%-22s %8s %10s %10s %10s", "statements per session", "batches", "p50", "p95", "p99")
	var p50 []time.Duration
	for b, took := range byBucket {
		if len(took) == 0 {
			continue
		}
		slices.Sort(took)
		pct := func(p float64) time.Duration { return took[int(p*float64(len(took)-1))] }
		t.Logf("%-22s %8d %10s %10s %10s",
			fmt.Sprintf("%d-%d", b*statements/buckets, (b+1)*statements/buckets),
			len(took), pct(0.50).Round(time.Millisecond), pct(0.95).Round(time.Millisecond), pct(0.99).Round(time.Millisecond))
		p50 = append(p50, pct(0.50))
	}
	require.NotEmpty(t, p50, "no batch was sent")
	return p50
}

// loadStatement is stmtBytes of SQL that compresses like real statements do,
// not like one repeated byte.
func loadStatement(stmtBytes int) string {
	b := make([]byte, max(stmtBytes/2, 1))
	_, _ = rand.Read(b)
	return "SELECT '" + hex.EncodeToString(b)[:max(stmtBytes-len("SELECT ''"), 1)] + "'"
}

func loadEnvInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	require.NoError(t, err, "%s", name)
	require.Positive(t, n, "%s", name)
	return n
}
