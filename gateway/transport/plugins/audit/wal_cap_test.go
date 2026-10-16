package audit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	eventlogv1 "github.com/hoophq/hoop/gateway/session/eventlog/v1"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// A result set larger than the stream cap must not be written to the WAL past
// the cap, must be flushed one bounded window at a time, and the session must
// persist the same stream as when it was neither.
func TestWALStopsWritingPastStreamCap(t *testing.T) {
	startTestDB(t)

	origAuditPath, origCap, origWindow := plugintypes.AuditPath, maxStreamBytes, flushWindowBytes
	t.Cleanup(func() { plugintypes.AuditPath, maxStreamBytes, flushWindowBytes = origAuditPath, origCap, origWindow })
	plugintypes.AuditPath = t.TempDir()
	maxStreamBytes = 4096
	flushWindowBytes = 1024

	orgID, sid := uuid.NewString(), uuid.NewString()
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'wal-cap-test')`, orgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
	startDate := time.Now().UTC()
	if err := models.UpsertSession(models.Session{ID: sid, OrgID: orgID, Connection: "bash",
		ConnectionType: "custom", Verb: "exec", Status: "open", CreatedAt: startDate}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	p := New()
	pctx := plugintypes.Context{OrgID: orgID, SID: sid, ConnectionType: "custom",
		ParamsData: plugintypes.GenericMap{"status": "open", "start_date": &startDate}}
	if err := p.writeOnConnect(pctx); err != nil {
		t.Fatalf("open wal: %v", err)
	}
	if err := models.CreateEmptySessionStreamBlob(orgID, sid, nil); err != nil {
		t.Fatalf("create stream blob: %v", err)
	}

	// 100 rows of 100 bytes: about 2.5x the cap once encoded.
	rows := make([][]byte, 100)
	for i := range rows {
		rows[i] = bytes.Repeat(fmt.Appendf(nil, "%04d", i), 25)
		if err := p.writeOnReceive(pctx, eventlogv1.OutputType, rows[i], nil); err != nil {
			t.Fatalf("write row %d: %v", i, err)
		}
	}

	// flushSession reads entries until it has read more than the cap, so it
	// persists rows[:wantRows] and nothing after.
	entry, _ := eventlogv1.New(startDate, eventlogv1.OutputType, rows[0], nil).Encode()
	wantRows := int(maxStreamBytes)/len(entry) + 1

	walogm := p.walSessionStore.Get(sid).(*walLogRWMutex)
	if got, want := walogm.log.Size(), int64(wantRows*len(entry)); got != want {
		t.Fatalf("wal size=%d, want %d: writes did not stop after the entry that crosses the cap", got, want)
	}

	// One window holds the entries read until they reach flushWindowBytes.
	header, err := walogm.log.Header()
	if err != nil {
		t.Fatal(err)
	}
	full, err := p.flushWindow(walogm, pctx, header, flushWindowBytes)
	if err != nil {
		t.Fatalf("flush window: %v", err)
	}
	perWindow := (int(flushWindowBytes) + len(entry) - 1) / len(entry)
	if got := int(walogm.lastFlushedIndex) - 1; !full || got != perWindow {
		t.Fatalf("first window flushed %d rows (full=%v), want %d rows and more left", got, full, perWindow)
	}

	if err := p.closeSessionWAL(pctx, nil); err != nil {
		t.Fatalf("close session: %v", err)
	}

	sess, err := models.GetSessionByID(orgID, sid)
	if err != nil {
		t.Fatal(err)
	}
	if truncated, _ := sess.Metrics["truncated"].(bool); !truncated {
		t.Errorf("metrics.truncated=%v, want true", sess.Metrics["truncated"])
	}
	blob, err := sess.GetBlobStream()
	if err != nil || blob == nil {
		t.Fatalf("read stream blob: blob=%v err=%v", blob, err)
	}
	var stream [][]any
	if err := json.Unmarshal(blob.BlobStream, &stream); err != nil {
		t.Fatalf("decode stream: %v", err)
	}
	if len(stream) != wantRows {
		t.Fatalf("persisted %d rows, want %d", len(stream), wantRows)
	}
	for i, ev := range stream {
		got, _ := base64.StdEncoding.DecodeString(ev[2].(string))
		if !bytes.Equal(got, rows[i]) {
			t.Fatalf("row %d: persisted %q, want %q", i, got, rows[i])
		}
	}
	// A flush larger than one window adds rows instead of rewriting the stream.
	if n := countStreamChunks(t, orgID, sid); n == 0 {
		t.Errorf("multi-window flush wrote %d chunk rows, want rows", n)
	}

	// A flush that fits one window keeps rewriting the stream, as before.
	small := uuid.NewString()
	if err := models.UpsertSession(models.Session{ID: small, OrgID: orgID, Connection: "bash",
		ConnectionType: "custom", Verb: "exec", Status: "open", CreatedAt: startDate}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	spctx := pctx
	spctx.SID = small
	if err := p.writeOnConnect(spctx); err != nil {
		t.Fatalf("open wal: %v", err)
	}
	if err := models.CreateEmptySessionStreamBlob(orgID, small, nil); err != nil {
		t.Fatalf("create stream blob: %v", err)
	}
	for _, row := range rows[:3] {
		if err := p.writeOnReceive(spctx, eventlogv1.OutputType, row, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.closeSessionWAL(spctx, nil); err != nil {
		t.Fatalf("close session: %v", err)
	}
	if n := countStreamChunks(t, orgID, small); n != 0 {
		t.Errorf("single-window flush wrote %d chunk rows, want 0", n)
	}
}

func countStreamChunks(t *testing.T, orgID, sid string) int64 {
	t.Helper()
	var n int64
	if err := models.DB.Raw(`SELECT count(*) FROM private.session_stream_chunks WHERE org_id = ? AND blob_id = ?`,
		orgID, models.SessionStreamBlobID(sid)).Scan(&n).Error; err != nil {
		t.Fatalf("count stream chunks: %v", err)
	}
	return n
}

// A flush must not hold the WAL while it writes to the database: the agent
// stream that feeds writeOnReceive carries every session of the agent.
func TestFlushDoesNotBlockWritesDuringAppend(t *testing.T) {
	origAuditPath, origAppend := plugintypes.AuditPath, appendStream
	t.Cleanup(func() { plugintypes.AuditPath, appendStream = origAuditPath, origAppend })
	plugintypes.AuditPath = t.TempDir()

	entered, release := make(chan struct{}), make(chan struct{})
	appendStream = func(_, _ string, _ json.RawMessage) error {
		close(entered)
		<-release
		return nil
	}

	startDate := time.Now().UTC()
	pctx := plugintypes.Context{OrgID: uuid.NewString(), SID: uuid.NewString(), ConnectionType: "custom",
		ParamsData: plugintypes.GenericMap{"status": "open", "start_date": &startDate}}
	p := New()
	if err := p.writeOnConnect(pctx); err != nil {
		t.Fatalf("open wal: %v", err)
	}
	// Cleanups run last-in first-out: release a blocked append before the WAL closes.
	t.Cleanup(func() { p.dropWalLog(pctx.SID) })
	releaseAppend := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseAppend)
	if err := p.writeOnReceive(pctx, eventlogv1.OutputType, []byte("row"), nil); err != nil {
		t.Fatal(err)
	}

	walogm := p.walSessionStore.Get(pctx.SID).(*walLogRWMutex)
	flushed := make(chan error, 1)
	go func() { flushed <- p.flushSession(walogm, pctx) }()
	<-entered

	wrote := make(chan error, 1)
	go func() { wrote <- p.writeOnReceive(pctx, eventlogv1.OutputType, []byte("next row"), nil) }()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writeOnReceive waited for the flush's database write")
	}
	releaseAppend()
	if err := <-flushed; err != nil {
		t.Fatal(err)
	}
}

// Writes, flushes and close at the same time must persist the written rows in
// order, without gaps or duplicates.
func TestFlushWhileWritingKeepsOrder(t *testing.T) {
	startTestDB(t)
	origAuditPath, origCap, origWindow := plugintypes.AuditPath, maxStreamBytes, flushWindowBytes
	t.Cleanup(func() { plugintypes.AuditPath, maxStreamBytes, flushWindowBytes = origAuditPath, origCap, origWindow })
	plugintypes.AuditPath = t.TempDir()
	flushWindowBytes = 64 << 10

	for _, streamCap := range []int64{1 << 20, 64 << 20} { // truncated, then whole
		maxStreamBytes = streamCap
		orgID, sid := uuid.NewString(), uuid.NewString()
		if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, orgID, "flush-order-"+orgID).Error; err != nil {
			t.Fatalf("seed org: %v", err)
		}
		startDate := time.Now().UTC()
		if err := models.UpsertSession(models.Session{ID: sid, OrgID: orgID, Connection: "bash",
			ConnectionType: "custom", Verb: "exec", Status: "open", CreatedAt: startDate}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		p := New()
		pctx := plugintypes.Context{OrgID: orgID, SID: sid, ConnectionType: "custom",
			ParamsData: plugintypes.GenericMap{"status": "open", "start_date": &startDate}}
		if err := p.writeOnConnect(pctx); err != nil {
			t.Fatalf("open wal: %v", err)
		}
		if err := models.CreateEmptySessionStreamBlob(orgID, sid, nil); err != nil {
			t.Fatalf("create stream blob: %v", err)
		}

		walogm := p.walSessionStore.Get(sid).(*walLogRWMutex)
		stop, flushed := make(chan struct{}), make(chan error, 1)
		go func() { // scheduled flushes, back to back
			for {
				select {
				case <-stop:
					flushed <- nil
					return
				default:
				}
				if err := p.flushSession(walogm, pctx); err != nil {
					flushed <- err
					return
				}
			}
		}()
		rows := make([][]byte, 600)
		for i := range rows {
			rows[i] = bytes.Repeat(fmt.Appendf(nil, "%05d", i), 2000)
			if err := p.writeOnReceive(pctx, eventlogv1.OutputType, rows[i], nil); err != nil {
				t.Fatalf("write row %d: %v", i, err)
			}
		}
		close(stop)
		if err := p.closeSessionWAL(pctx, nil); err != nil {
			t.Fatalf("close session: %v", err)
		}
		if err := <-flushed; err != nil {
			t.Fatalf("flush: %v", err)
		}

		sess, err := models.GetSessionByID(orgID, sid)
		if err != nil {
			t.Fatal(err)
		}
		blob, err := sess.GetBlobStream()
		if err != nil || blob == nil {
			t.Fatalf("read stream blob: blob=%v err=%v", blob, err)
		}
		var stream [][]any
		if err := json.Unmarshal(blob.BlobStream, &stream); err != nil {
			t.Fatalf("decode stream: %v", err)
		}
		truncated, _ := sess.Metrics["truncated"].(bool)
		if len(stream) == 0 || len(stream) > len(rows) || truncated != (len(stream) < len(rows)) {
			t.Fatalf("cap %d: persisted %d of %d rows, truncated=%v", streamCap, len(stream), len(rows), truncated)
		}
		for i, ev := range stream {
			got, _ := base64.StdEncoding.DecodeString(ev[2].(string))
			if !bytes.Equal(got, rows[i]) {
				t.Fatalf("cap %d: event %d is not row %d", streamCap, i, i)
			}
		}
	}
}

// A write that lands while the final flush writes to the database must be
// persisted, not removed with the WAL.
func TestCloseKeepsWriteDuringFinalFlush(t *testing.T) {
	startTestDB(t)
	origAuditPath, origAppend := plugintypes.AuditPath, appendStream
	t.Cleanup(func() { plugintypes.AuditPath, appendStream = origAuditPath, origAppend })
	plugintypes.AuditPath = t.TempDir()

	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	appendStream = func(orgID, sid string, entries json.RawMessage) error {
		once.Do(func() {
			close(entered)
			<-release
		})
		return models.AppendSessionStream(orgID, sid, entries)
	}

	orgID, sid := uuid.NewString(), uuid.NewString()
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'close-race-test')`, orgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
	startDate := time.Now().UTC()
	if err := models.UpsertSession(models.Session{ID: sid, OrgID: orgID, Connection: "bash",
		ConnectionType: "custom", Verb: "exec", Status: "open", CreatedAt: startDate}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	p := New()
	pctx := plugintypes.Context{OrgID: orgID, SID: sid, ConnectionType: "custom",
		ParamsData: plugintypes.GenericMap{"status": "open", "start_date": &startDate}}
	if err := p.writeOnConnect(pctx); err != nil {
		t.Fatalf("open wal: %v", err)
	}
	if err := models.CreateEmptySessionStreamBlob(orgID, sid, nil); err != nil {
		t.Fatalf("create stream blob: %v", err)
	}
	if err := p.writeOnReceive(pctx, eventlogv1.OutputType, []byte("first"), nil); err != nil {
		t.Fatal(err)
	}

	// a receive that took the WAL before close popped it from the store
	walogm := p.walSessionStore.Get(sid).(*walLogRWMutex)
	closed := make(chan error, 1)
	go func() { closed <- p.closeSessionWAL(pctx, nil) }()
	<-entered
	if _, err := walogm.write(eventlogv1.OutputType, []byte("late"), nil); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatalf("close session: %v", err)
	}

	sess, err := models.GetSessionByID(orgID, sid)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := sess.GetBlobStream()
	if err != nil || blob == nil {
		t.Fatalf("read stream blob: blob=%v err=%v", blob, err)
	}
	var stream [][]any
	if err := json.Unmarshal(blob.BlobStream, &stream); err != nil {
		t.Fatalf("decode stream: %v", err)
	}
	var got []string
	for _, ev := range stream {
		b, _ := base64.StdEncoding.DecodeString(ev[2].(string))
		got = append(got, string(b))
	}
	if strings.Join(got, ",") != "first,late" {
		t.Fatalf("stored %q, want [first late]", got)
	}
}

// With AuditStreamChunksFlag off, a flush appends its whole tail once to the
// stream, as before the flag, and stores the same rows.
func TestFlushWithoutChunksFlagAppendsOnce(t *testing.T) {
	startTestDB(t)
	origAuditPath, origCap, origWindow, origAppend := plugintypes.AuditPath, maxStreamBytes, flushWindowBytes, appendStream
	t.Cleanup(func() {
		plugintypes.AuditPath, maxStreamBytes, flushWindowBytes, appendStream = origAuditPath, origCap, origWindow, origAppend
	})
	plugintypes.AuditPath = t.TempDir()
	maxStreamBytes, flushWindowBytes = 4096, 1024
	appends := 0
	appendStream = func(orgID, sid string, entries json.RawMessage) error {
		appends++
		return models.AppendSessionStream(orgID, sid, entries)
	}

	orgID, sid := uuid.NewString(), uuid.NewString()
	featureflag.Set(orgID, AuditStreamChunksFlag, false)
	t.Cleanup(func() { featureflag.SetAll(orgID, map[string]bool{}) })
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'chunks-flag-off-test')`, orgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
	startDate := time.Now().UTC()
	if err := models.UpsertSession(models.Session{ID: sid, OrgID: orgID, Connection: "bash",
		ConnectionType: "custom", Verb: "exec", Status: "open", CreatedAt: startDate}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	p := New()
	pctx := plugintypes.Context{OrgID: orgID, SID: sid, ConnectionType: "custom",
		ParamsData: plugintypes.GenericMap{"status": "open", "start_date": &startDate}}
	if err := p.writeOnConnect(pctx); err != nil {
		t.Fatalf("open wal: %v", err)
	}
	if err := models.CreateEmptySessionStreamBlob(orgID, sid, nil); err != nil {
		t.Fatalf("create stream blob: %v", err)
	}
	rows := make([][]byte, 100)
	for i := range rows {
		rows[i] = bytes.Repeat(fmt.Appendf(nil, "%04d", i), 25)
		if err := p.writeOnReceive(pctx, eventlogv1.OutputType, rows[i], nil); err != nil {
			t.Fatalf("write row %d: %v", i, err)
		}
	}
	if err := p.closeSessionWAL(pctx, nil); err != nil {
		t.Fatalf("close session: %v", err)
	}

	if appends != 1 {
		t.Errorf("flush appended %d times, want 1", appends)
	}
	if n := countStreamChunks(t, orgID, sid); n != 0 {
		t.Errorf("wrote %d chunk rows with the flag off, want 0", n)
	}
	sess, err := models.GetSessionByID(orgID, sid)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := sess.GetBlobStream()
	if err != nil || blob == nil {
		t.Fatalf("read stream blob: blob=%v err=%v", blob, err)
	}
	var stream [][]any
	if err := json.Unmarshal(blob.BlobStream, &stream); err != nil {
		t.Fatalf("decode stream: %v", err)
	}
	entry, _ := eventlogv1.New(startDate, eventlogv1.OutputType, rows[0], nil).Encode()
	if want := int(maxStreamBytes)/len(entry) + 1; len(stream) != want {
		t.Fatalf("persisted %d rows, want %d", len(stream), want)
	}
	for i, ev := range stream {
		got, _ := base64.StdEncoding.DecodeString(ev[2].(string))
		if !bytes.Equal(got, rows[i]) {
			t.Fatalf("row %d: persisted %q, want %q", i, got, rows[i])
		}
	}
}

// Turning AuditStreamChunksFlag on or off between two flushes of a session
// keeps its stored rows in order. A session with chunk rows keeps writing rows.
func TestChunksFlagFlipBetweenFlushesKeepsOrder(t *testing.T) {
	startTestDB(t)
	origAuditPath, origCap, origWindow := plugintypes.AuditPath, maxStreamBytes, flushWindowBytes
	t.Cleanup(func() { plugintypes.AuditPath, maxStreamBytes, flushWindowBytes = origAuditPath, origCap, origWindow })
	plugintypes.AuditPath = t.TempDir()
	maxStreamBytes, flushWindowBytes = 1<<20, 1024

	for _, tc := range []struct {
		name       string
		on1, on2   bool
		wantChunks bool
	}{
		{"on then off", true, false, true},
		{"off then on", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orgID, sid := uuid.NewString(), uuid.NewString()
			t.Cleanup(func() { featureflag.SetAll(orgID, map[string]bool{}) })
			if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, orgID, "flag-flip-"+orgID).Error; err != nil {
				t.Fatalf("seed org: %v", err)
			}
			startDate := time.Now().UTC()
			if err := models.UpsertSession(models.Session{ID: sid, OrgID: orgID, Connection: "bash",
				ConnectionType: "custom", Verb: "exec", Status: "open", CreatedAt: startDate}); err != nil {
				t.Fatalf("seed session: %v", err)
			}
			p := New()
			pctx := plugintypes.Context{OrgID: orgID, SID: sid, ConnectionType: "custom",
				ParamsData: plugintypes.GenericMap{"status": "open", "start_date": &startDate}}
			if err := p.writeOnConnect(pctx); err != nil {
				t.Fatalf("open wal: %v", err)
			}
			if err := models.CreateEmptySessionStreamBlob(orgID, sid, nil); err != nil {
				t.Fatalf("create stream blob: %v", err)
			}
			walogm := p.walSessionStore.Get(sid).(*walLogRWMutex)

			rows := make([][]byte, 60)
			for i := range rows {
				rows[i] = bytes.Repeat(fmt.Appendf(nil, "%04d", i), 25)
			}
			for half, on := range []bool{tc.on1, tc.on2} {
				featureflag.Set(orgID, AuditStreamChunksFlag, on)
				for _, row := range rows[half*30 : half*30+30] {
					if err := p.writeOnReceive(pctx, eventlogv1.OutputType, row, nil); err != nil {
						t.Fatal(err)
					}
				}
				if err := p.flushSession(walogm, pctx); err != nil {
					t.Fatalf("flush %d: %v", half, err)
				}
			}
			if err := p.closeSessionWAL(pctx, nil); err != nil {
				t.Fatalf("close session: %v", err)
			}

			if n := countStreamChunks(t, orgID, sid); (n > 0) != tc.wantChunks {
				t.Errorf("chunk rows=%d, want rows=%v", n, tc.wantChunks)
			}
			sess, err := models.GetSessionByID(orgID, sid)
			if err != nil {
				t.Fatal(err)
			}
			blob, err := sess.GetBlobStream()
			if err != nil || blob == nil {
				t.Fatalf("read stream blob: blob=%v err=%v", blob, err)
			}
			var stream [][]any
			if err := json.Unmarshal(blob.BlobStream, &stream); err != nil {
				t.Fatalf("decode stream: %v", err)
			}
			if len(stream) != len(rows) {
				t.Fatalf("persisted %d rows, want %d", len(stream), len(rows))
			}
			for i, ev := range stream {
				got, _ := base64.StdEncoding.DecodeString(ev[2].(string))
				if !bytes.Equal(got, rows[i]) {
					t.Fatalf("row %d: persisted %q, want %q", i, got, rows[i])
				}
			}
		})
	}
}

func startTestDB(t *testing.T) {
	t.Helper()
	inst := pglitetest.StartMigrated(t)
	// The embedded backend serves one session at a time.
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := models.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
}
