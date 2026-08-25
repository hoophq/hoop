// Package sqlite implements store.Store over a local SQLite file.
//
// # Choosing SQLite
//
// The audit trail of a sidecar is a single-writer, many-reader workload with
// a working set measured in megabytes. SQLite handles that shape well, and
// it needs no server to operate, no credentials to leak, and no network hop
// on the connection's data path. A deployment that outgrows it swaps in a
// Postgres backend behind the same store.Store interface.
//
// # The nested module
//
// The root hoopinspect module has zero dependencies and must keep them. This
// package needs a driver, so it lives behind its own go.mod. See go.mod.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/hoopinspect"
	"github.com/hoophq/hoop/hoopinspect/audit"
	"github.com/hoophq/hoop/hoopinspect/session"
	"github.com/hoophq/hoop/hoopinspect/store"

	_ "modernc.org/sqlite"
)

// Store is a SQLite-backed audit store. It satisfies both audit.Sink (the
// write path the gate drives) and store.Store (the read path a UI drives).
type Store struct {
	db *sql.DB

	// keepAlive pins one connection open for an in-memory database. A
	// shared-cache memory database is destroyed the moment its LAST
	// connection closes, and database/sql is free to retire idle
	// connections; without this pin the schema can vanish between two
	// queries. Nil for a file database, which needs no such anchor.
	keepAlive *sql.Conn

	// writeMu serializes Write. Correctness does not depend on it, since the
	// transaction below already makes the event insert and the session
	// upsert atomic. Throughput does. SQLite admits exactly one writer at a
	// time even in WAL mode, so unsynchronized goroutines pile onto the
	// database lock and each retries under busy_timeout. Queueing them on a
	// Go mutex instead measured ~4x faster for 1280 concurrent writes
	// (253ms vs 976ms, 32 goroutines), and this runs on the connection's
	// data path where a blocked write blocks the user's query.
	writeMu sync.Mutex

	closeOnce sync.Once
	closeErr  error
}

var (
	_ audit.Sink  = (*Store)(nil)
	_ store.Store = (*Store)(nil)
)

// Open opens or creates the database at path.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("hoopinspect/store/sqlite: empty path")
	}
	return open(dsn(path, false), false)
}

// OpenMemory opens a private in-memory database, for tests.
//
// The DSN names the database and sets cache=shared rather than using a bare
// ":memory:". database/sql keeps a POOL of connections, and a bare in-memory
// database is per-connection: the pool's second connection would see an empty
// schema, which surfaces as a test that cannot find its own tables. A shared
// cache makes every connection in the pool see one database.
func OpenMemory() (*Store, error) {
	// A unique name per call so two stores in the same test binary do not
	// share a database and see each other's rows.
	return open(dsn(fmt.Sprintf("memdb-%d", nextMemDB()), true), true)
}

var memDBSeq struct {
	sync.Mutex
	n int64
}

func nextMemDB() int64 {
	memDBSeq.Lock()
	defer memDBSeq.Unlock()
	memDBSeq.n++
	return memDBSeq.n
}

// dsn builds the connection string.
//
// Each pragma earns its place:
//
//   - journal_mode=WAL lets readers run while a write is in flight. Without
//     it a dashboard refresh blocks the connection recording a statement,
//     which an audit path must never do. WAL is unavailable for in-memory
//     databases, so it is set only on file DSNs.
//   - busy_timeout gives a writer five seconds to acquire the lock instead of
//     failing at once with SQLITE_BUSY. A checkpoint or a concurrent process
//     holding the lock for a few milliseconds must not turn into a lost audit
//     record.
//   - foreign_keys is on for correctness if a future migration adds one.
//   - synchronous=NORMAL is the documented safe pairing with WAL: durable
//     against process crash, and it avoids an fsync per transaction on a path
//     that runs once per statement.
func dsn(name string, memory bool) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	if memory {
		return "file:" + name + "?mode=memory&cache=shared&" + q.Encode()
	}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + name + "?" + q.Encode()
}

func open(dataSource string, memory bool) (*Store, error) {
	db, err := sql.Open("sqlite", dataSource)
	if err != nil {
		return nil, fmt.Errorf("hoopinspect/store/sqlite: open: %w", err)
	}

	ctx := context.Background()
	s := &Store{db: db}
	if memory {
		if s.keepAlive, err = db.Conn(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("hoopinspect/store/sqlite: pin memory connection: %w", err)
		}
	}
	if err := applySchema(ctx, db); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// DB exposes the underlying handle for operational tasks (VACUUM, backup).
// Callers must not write through it: the denormalized session counters are
// maintained by Write and a direct insert would desynchronize them.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database. Idempotent, per audit.Sink.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		if s.keepAlive != nil {
			// Released before the pool so the memory database is torn down
			// once, here, rather than leaking until GC.
			s.closeErr = s.keepAlive.Close()
		}
		if err := s.db.Close(); err != nil && s.closeErr == nil {
			s.closeErr = err
		}
	})
	return s.closeErr
}

// ---------------------------------------------------------------- write path

// Write records an event and folds it into the session row.
//
// The session row carries denormalized counters because a session list is the
// first screen of any audit UI, and computing "how many statements, how many
// denied" with a correlated subquery per row makes that screen slow enough
// that you stop opening it. Maintaining them here costs one UPSERT on a path
// already doing an INSERT.
func (s *Store) Write(ctx context.Context, ev audit.Event) error {
	if ev.SessionID == "" {
		return errors.New("hoopinspect/store/sqlite: event has no session id")
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("hoopinspect/store/sqlite: begin: %w", err)
	}
	defer tx.Rollback()

	if err := insertEvent(ctx, tx, ev); err != nil {
		return err
	}
	if err := upsertSession(ctx, tx, ev); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("hoopinspect/store/sqlite: commit: %w", err)
	}
	return nil
}

func insertEvent(ctx context.Context, tx *sql.Tx, ev audit.Event) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO events (
	session_id, kind, timestamp, principal, protocol, connection, operation,
	statement, tables, allowed, rule, message, direction, masked_entities,
	masked_count, error, duration_ns, statement_count, denied_count, http, metadata
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(ev.SessionID), string(ev.Kind), micros(ev.Timestamp), ev.Principal,
		string(ev.Protocol), ev.Connection, string(ev.Operation), ev.Statement,
		encodeJSON(ev.Tables), boolInt(ev.Allowed), ev.Rule, ev.Message,
		string(ev.Direction), encodeJSON(ev.MaskedEntities), ev.MaskedCount,
		ev.Error, int64(ev.Duration), ev.StatementCount, ev.DeniedCount,
		encodeJSON(ev.HTTP), encodeJSON(ev.Metadata))
	if err != nil {
		return fmt.Errorf("hoopinspect/store/sqlite: insert event: %w", err)
	}
	return nil
}

// upsertSession folds one event into the session row.
//
// The INSERT branch handles an event arriving with no preceding
// session_start: a sink attached mid-session must not drop the event, and a
// timeline missing its parent row is invisible to the list view. The seed
// values are the deltas this event contributes, so the row is correct from
// its first event whichever kind that was.
func upsertSession(ctx context.Context, tx *sql.Tx, ev audit.Event) error {
	var stmtDelta, deniedDelta, maskedDelta, errorDelta int
	switch ev.Kind {
	case audit.KindStatement:
		stmtDelta = 1
	case audit.KindViolation:
		// A violation is also a statement that was attempted. Counting it in
		// both keeps statement_count meaning "statements seen" rather than
		// "statements allowed", the reading an auditor expects.
		stmtDelta, deniedDelta = 1, 1
	case audit.KindMasked:
		// MaskedCount is how many VALUES were rewritten; a masked event with
		// no count still represents one masking action.
		maskedDelta = max(ev.MaskedCount, 1)
	case audit.KindError:
		errorDelta = 1
	}

	// session_end carries authoritative totals from the gate. Prefer them
	// over the accumulated counters: an AsyncSink may have dropped events
	// under backpressure, and the gate's own tally saw every statement.
	var endedAt, durationMS int64
	finalStmts, finalDenied := -1, -1
	if ev.Kind == audit.KindSessionEnd {
		endedAt = micros(ev.Timestamp)
		durationMS = ev.Duration.Milliseconds()
		if ev.StatementCount > 0 || ev.DeniedCount > 0 {
			finalStmts, finalDenied = ev.StatementCount, ev.DeniedCount
		}
	}

	risk := ev.Metadata["risk_level"]

	// RETURNING gives the post-upsert counters, so the verdict below costs no
	// extra read.
	var denied, errCount int
	err := tx.QueryRowContext(ctx, `
INSERT INTO sessions (
	id, principal, protocol, connection, upstream, started_at, ended_at,
	duration_ms, statement_count, denied_count, masked_count, error_count,
	verdict, risk_level, metadata
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	-- COALESCE-style fill: later events carry the same facts, but a session
	-- seeded by a bare statement event may be missing ones session_start
	-- would have supplied.
	principal  = CASE WHEN sessions.principal  = '' THEN excluded.principal  ELSE sessions.principal  END,
	protocol   = CASE WHEN sessions.protocol   = '' THEN excluded.protocol   ELSE sessions.protocol   END,
	connection = CASE WHEN sessions.connection = '' THEN excluded.connection ELSE sessions.connection END,
	upstream   = CASE WHEN sessions.upstream   = '' THEN excluded.upstream   ELSE sessions.upstream   END,
	metadata   = CASE WHEN sessions.metadata   = '' THEN excluded.metadata   ELSE sessions.metadata   END,

	-- An out-of-order event must not move the start later than the earliest
	-- timestamp seen, or the session sorts into the wrong place in the list.
	started_at = MIN(sessions.started_at, excluded.started_at),

	ended_at    = CASE WHEN ?  > 0 THEN ?  ELSE sessions.ended_at    END,
	duration_ms = CASE WHEN ?  > 0 THEN ?  ELSE sessions.duration_ms END,

	statement_count = CASE WHEN ? >= 0 THEN ? ELSE sessions.statement_count + ? END,
	denied_count    = CASE WHEN ? >= 0 THEN ? ELSE sessions.denied_count    + ? END,
	masked_count    = sessions.masked_count + ?,
	error_count     = sessions.error_count  + ?,

	-- Highest risk wins. Ranked by severity rather than compared as strings:
	-- 'high' < 'low' lexically, so MAX() on the raw value reports the wrong
	-- level. The incoming rank is computed in Go (riskRank) and the stored
	-- one by this CASE, so the ordering is stated once per side and nowhere
	-- else.
	risk_level = CASE
		WHEN ? > (CASE sessions.risk_level
		            WHEN 'low' THEN 1 WHEN 'medium' THEN 2 WHEN 'high' THEN 3
		            ELSE 0 END)
		THEN ? ELSE sessions.risk_level END
RETURNING denied_count, error_count`,
		// INSERT values.
		string(ev.SessionID), ev.Principal, string(ev.Protocol), ev.Connection,
		ev.Metadata["upstream"], micros(ev.Timestamp), endedAt, durationMS,
		stmtDelta, deniedDelta, maskedDelta, errorDelta,
		store.ClassifyVerdict(deniedDelta, errorDelta), risk, encodeJSON(ev.Metadata),
		// UPDATE parameters, in the order the CASE expressions consume them.
		endedAt, endedAt,
		durationMS, durationMS,
		finalStmts, finalStmts, stmtDelta,
		finalDenied, finalDenied, deniedDelta,
		maskedDelta, errorDelta,
		riskRank(risk), risk,
	).Scan(&denied, &errCount)
	if err != nil {
		return fmt.Errorf("hoopinspect/store/sqlite: upsert session: %w", err)
	}

	// The verdict is a pure function of the final counters, so it is derived
	// in one place (store.ClassifyVerdict) rather than reimplemented in SQL
	// where the precedence rule would drift from the Go one. RETURNING hands
	// back the post-upsert counters, so this costs no extra read.
	_, err = tx.ExecContext(ctx, `UPDATE sessions SET verdict = ? WHERE id = ?`,
		store.ClassifyVerdict(denied, errCount), string(ev.SessionID))
	if err != nil {
		return fmt.Errorf("hoopinspect/store/sqlite: set verdict: %w", err)
	}
	return nil
}

// riskRank orders risk levels by severity, matching the CASE in the upsert.
func riskRank(level string) int64 {
	switch level {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	}
	return 0
}

// ----------------------------------------------------------------- read path

// sessionCursor is the keyset position in a session listing.
//
// Keyset rather than OFFSET: OFFSET counts rows from the start of the result
// set, so a session opening between two page fetches shifts every later row
// down one and the reader skips it with no warning (or, on a delete, sees
// one twice). On a live audit trail that is the steady state. A keyset
// cursor names the last row seen, so a concurrent insert lands outside the
// window already read.
type sessionCursor struct {
	StartedAt int64  `json:"s"`
	ID        string `json:"i"`
}

// eventCursor pages events on seq alone. Millisecond (and even microsecond)
// timestamps collide under load, leaving seq, a monotonic AUTOINCREMENT, as
// the only total order available.
type eventCursor struct {
	Seq int64 `json:"q"`
}

var errBadCursor = errors.New("hoopinspect/store/sqlite: malformed cursor")

func encodeCursor(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// The cursor structs are two fixed-shape structs of scalars; marshal
		// cannot fail. Returning "" would restart paging with no signal.
		panic("hoopinspect/store/sqlite: cursor marshal: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor rejects a malformed cursor with an error rather than
// restarting from page one. Silently rewinding turns a client bug into an
// infinite loop that re-reads the first page forever.
func decodeCursor(s string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("%w: %v", errBadCursor, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", errBadCursor, err)
	}
	// Reject trailing bytes: a cursor with a payload appended is not one we
	// issued, and accepting it means accepting a forged position.
	if dec.More() {
		return fmt.Errorf("%w: trailing data", errBadCursor)
	}
	return nil
}

// sessionWhere builds the shared predicate for Sessions and Stats, so the
// dashboard aggregates over exactly the rows the list shows.
// It returns the conditions unjoined so a caller can add its own without
// string-surgery on an already-assembled clause.
func sessionWhere(f store.SessionFilter) ([]string, []any) {
	var where []string
	var args []any

	if f.Principal != "" {
		where = append(where, "principal = ?")
		args = append(args, f.Principal)
	}
	if f.Connection != "" {
		where = append(where, "connection = ?")
		args = append(args, f.Connection)
	}
	if f.Protocol != "" {
		where = append(where, "protocol = ?")
		args = append(args, string(f.Protocol))
	}
	if !f.Since.IsZero() {
		where = append(where, "started_at >= ?")
		args = append(args, micros(f.Since))
	}
	if !f.Until.IsZero() {
		// Until is exclusive, per the filter's documentation.
		where = append(where, "started_at < ?")
		args = append(args, micros(f.Until))
	}
	if f.DeniedOnly {
		where = append(where, "denied_count > 0")
	}
	if f.OpenOnly {
		where = append(where, "ended_at = 0")
	}
	if f.Search != "" {
		// Search covers principal, connection and statement text. The
		// statement half needs the events table, so it is an EXISTS rather
		// than a join: a join would multiply the session row by its events
		// and require a DISTINCT that defeats the paging index.
		where = append(where, `(
			LOWER(principal) LIKE ? ESCAPE '\'
			OR LOWER(connection) LIKE ? ESCAPE '\'
			OR EXISTS (SELECT 1 FROM events e WHERE e.session_id = sessions.id
			           AND LOWER(e.statement) LIKE ? ESCAPE '\')
		)`)
		pat := likePattern(f.Search)
		args = append(args, pat, pat, pat)
	}

	return where, args
}

// whereClause joins conditions, yielding "" when there are none so it can be
// concatenated onto any FROM unconditionally.
func whereClause(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

const sessionColumns = `id, principal, protocol, connection, upstream, started_at,
	ended_at, duration_ms, statement_count, denied_count, masked_count,
	error_count, verdict, risk_level, metadata`

// Sessions lists sessions newest first, paging on (started_at, id) descending.
func (s *Store) Sessions(ctx context.Context, f store.SessionFilter) (store.SessionPage, error) {
	f = f.Normalize()

	conds, args := sessionWhere(f)
	where := whereClause(conds)

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`+where, args...).Scan(&total); err != nil {
		return store.SessionPage{}, fmt.Errorf("hoopinspect/store/sqlite: count sessions: %w", err)
	}

	pageWhere, pageArgs := where, args
	if f.Cursor != "" {
		var c sessionCursor
		if err := decodeCursor(f.Cursor, &c); err != nil {
			return store.SessionPage{}, err
		}
		// Row-value comparison expresses "strictly before (started_at, id) in
		// descending order" as one term the composite index can range-scan.
		pageWhere = whereClause(append(append([]string{}, conds...), "(started_at, id) < (?, ?)"))
		pageArgs = append(append([]any{}, args...), c.StartedAt, c.ID)
	}

	// Fetch one extra row to learn whether another page exists without a
	// second query, and without reporting a cursor that leads to an empty
	// page.
	query := `SELECT ` + sessionColumns + ` FROM sessions` + pageWhere +
		` ORDER BY started_at DESC, id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, append(pageArgs, f.Limit+1)...)
	if err != nil {
		return store.SessionPage{}, fmt.Errorf("hoopinspect/store/sqlite: query sessions: %w", err)
	}
	defer rows.Close()

	page := store.SessionPage{Total: total}
	var lastStarted int64
	for rows.Next() {
		rec, started, err := scanSession(rows)
		if err != nil {
			return store.SessionPage{}, err
		}
		if len(page.Sessions) == f.Limit {
			page.NextCursor = encodeCursor(sessionCursor{StartedAt: lastStarted, ID: string(page.Sessions[f.Limit-1].ID)})
			break
		}
		page.Sessions = append(page.Sessions, rec)
		lastStarted = started
	}
	if err := rows.Err(); err != nil {
		return store.SessionPage{}, fmt.Errorf("hoopinspect/store/sqlite: scan sessions: %w", err)
	}
	return page, nil
}

// Session returns one session, or store.ErrNotFound.
func (s *Store) Session(ctx context.Context, id session.ID) (store.SessionRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, string(id))
	rec, _, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return store.SessionRecord{}, fmt.Errorf("session %q: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return store.SessionRecord{}, err
	}
	return rec, nil
}

// scanner abstracts *sql.Row and *sql.Rows so one scan body serves both.
type scanner interface{ Scan(dest ...any) error }

func scanSession(sc scanner) (store.SessionRecord, int64, error) {
	var (
		rec                store.SessionRecord
		id, proto          string
		startedAt, endedAt int64
		metadata           string
	)
	err := sc.Scan(&id, &rec.Principal, &proto, &rec.Connection, &rec.Upstream,
		&startedAt, &endedAt, &rec.DurationMS, &rec.StatementCount,
		&rec.DeniedCount, &rec.MaskedCount, &rec.ErrorCount, &rec.Verdict,
		&rec.RiskLevel, &metadata)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.SessionRecord{}, 0, err
		}
		return store.SessionRecord{}, 0, fmt.Errorf("hoopinspect/store/sqlite: scan session: %w", err)
	}
	rec.ID = session.ID(id)
	rec.Protocol = hoopinspect.Protocol(proto)
	rec.StartedAt = fromMicros(startedAt)
	rec.EndedAt = fromMicros(endedAt)
	if err := decodeJSON(metadata, &rec.Metadata); err != nil {
		return store.SessionRecord{}, 0, err
	}
	return rec, startedAt, nil
}

func eventWhere(f store.EventFilter) ([]string, []any) {
	var where []string
	var args []any

	if f.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, string(f.SessionID))
	}
	if len(f.Kinds) > 0 {
		ph := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			ph[i] = "?"
			args = append(args, string(k))
		}
		where = append(where, "kind IN ("+strings.Join(ph, ",")+")")
	}
	if f.Principal != "" {
		where = append(where, "principal = ?")
		args = append(args, f.Principal)
	}
	if f.Connection != "" {
		where = append(where, "connection = ?")
		args = append(args, f.Connection)
	}
	if f.Protocol != "" {
		where = append(where, "protocol = ?")
		args = append(args, string(f.Protocol))
	}
	if f.DeniedOnly {
		where = append(where, "kind = ?")
		args = append(args, string(audit.KindViolation))
	}
	if !f.Since.IsZero() {
		where = append(where, "timestamp >= ?")
		args = append(args, micros(f.Since))
	}
	if !f.Until.IsZero() {
		where = append(where, "timestamp < ?")
		args = append(args, micros(f.Until))
	}
	if f.Search != "" {
		where = append(where, `LOWER(statement) LIKE ? ESCAPE '\'`)
		args = append(args, likePattern(f.Search))
	}

	return where, args
}

const eventColumns = `seq, session_id, kind, timestamp, principal, protocol,
	connection, operation, statement, tables, allowed, rule, message, direction,
	masked_entities, masked_count, error, duration_ns, statement_count,
	denied_count, http, metadata`

// Events lists events oldest first, paging on seq ascending so a timeline
// reads top to bottom and a concurrent insert appends beyond the window
// already read.
func (s *Store) Events(ctx context.Context, f store.EventFilter) (store.EventPage, error) {
	f = f.Normalize()

	conds, args := eventWhere(f)
	where := whereClause(conds)

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`+where, args...).Scan(&total); err != nil {
		return store.EventPage{}, fmt.Errorf("hoopinspect/store/sqlite: count events: %w", err)
	}

	pageWhere, pageArgs := where, args
	if f.Cursor != "" {
		var c eventCursor
		if err := decodeCursor(f.Cursor, &c); err != nil {
			return store.EventPage{}, err
		}
		pageWhere = whereClause(append(append([]string{}, conds...), "seq > ?"))
		pageArgs = append(append([]any{}, args...), c.Seq)
	}

	query := `SELECT ` + eventColumns + ` FROM events` + pageWhere + ` ORDER BY seq ASC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, append(pageArgs, f.Limit+1)...)
	if err != nil {
		return store.EventPage{}, fmt.Errorf("hoopinspect/store/sqlite: query events: %w", err)
	}
	defer rows.Close()

	page := store.EventPage{Total: total}
	for rows.Next() {
		rec, err := scanEvent(rows)
		if err != nil {
			return store.EventPage{}, err
		}
		if len(page.Events) == f.Limit {
			page.NextCursor = encodeCursor(eventCursor{Seq: page.Events[f.Limit-1].Seq})
			break
		}
		page.Events = append(page.Events, rec)
	}
	if err := rows.Err(); err != nil {
		return store.EventPage{}, fmt.Errorf("hoopinspect/store/sqlite: scan events: %w", err)
	}
	return page, nil
}

func scanEvent(sc scanner) (store.EventRecord, error) {
	var (
		rec                                store.EventRecord
		sid, kind, proto, op, dir          string
		ts, durationNS                     int64
		allowed                            int
		tablesJSON, entitiesJSON           string
		httpJSON, metaJSON                 string
		statementCount, deniedCount, maskC int
	)
	err := sc.Scan(&rec.Seq, &sid, &kind, &ts, &rec.Principal, &proto,
		&rec.Connection, &op, &rec.Statement, &tablesJSON, &allowed, &rec.Rule,
		&rec.Message, &dir, &entitiesJSON, &maskC, &rec.Error, &durationNS,
		&statementCount, &deniedCount, &httpJSON, &metaJSON)
	if err != nil {
		return store.EventRecord{}, fmt.Errorf("hoopinspect/store/sqlite: scan event: %w", err)
	}

	rec.SessionID = session.ID(sid)
	rec.Kind = audit.Kind(kind)
	rec.Timestamp = fromMicros(ts)
	rec.Protocol = hoopinspect.Protocol(proto)
	rec.Operation = hoopinspect.Operation(op)
	rec.Direction = hoopinspect.Direction(dir)
	rec.Allowed = allowed != 0
	rec.MaskedCount = maskC
	rec.Duration = time.Duration(durationNS)
	rec.StatementCount = statementCount
	rec.DeniedCount = deniedCount

	if err := decodeJSON(tablesJSON, &rec.Tables); err != nil {
		return store.EventRecord{}, err
	}
	if err := decodeJSON(entitiesJSON, &rec.MaskedEntities); err != nil {
		return store.EventRecord{}, err
	}
	if err := decodeJSON(httpJSON, &rec.HTTP); err != nil {
		return store.EventRecord{}, err
	}
	if err := decodeJSON(metaJSON, &rec.Metadata); err != nil {
		return store.EventRecord{}, err
	}
	return rec, nil
}

// Stats aggregates over the same rows Sessions would list, so a dashboard and
// the list underneath it never disagree.
func (s *Store) Stats(ctx context.Context, f store.SessionFilter) (store.Stats, error) {
	conds, args := sessionWhere(f)
	where := whereClause(conds)

	st := store.Stats{Since: f.Since, Until: f.Until}

	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(statement_count), 0),
       COALESCE(SUM(denied_count), 0),
       COALESCE(SUM(masked_count), 0),
       COALESCE(SUM(error_count), 0)
FROM sessions`+where, args...).
		Scan(&st.Sessions, &st.Statements, &st.Denied, &st.Masked, &st.Errors)
	if err != nil {
		return store.Stats{}, fmt.Errorf("hoopinspect/store/sqlite: stats totals: %w", err)
	}

	// sessionBreakdown counts SESSIONS per label; the operation and rule
	// breakdowns below count EVENTS. Keep the two units apart: "12 sessions
	// by alice" and "12 selects" are different facts, and a chart that
	// conflated them would be wrong.
	sessionBreakdown := func(column string) ([]store.LabelCount, error) {
		q := `SELECT ` + column + `, COUNT(*) FROM sessions` +
			whereClause(append(append([]string{}, conds...), column+` <> ''`)) +
			` GROUP BY ` + column
		return s.labelCounts(ctx, q, args)
	}
	// The event breakdowns restrict to the sessions the filter selected, so
	// the dashboard's charts and its list agree on the population. IN over a
	// subquery rather than a join: the session row contributes no columns
	// here, and a join would need a DISTINCT that defeats the index.
	eventBreakdown := func(column string) ([]store.LabelCount, error) {
		q := `SELECT ` + column + `, COUNT(*) FROM events WHERE ` + column +
			` <> '' AND session_id IN (SELECT id FROM sessions` + where + `) GROUP BY ` + column
		return s.labelCounts(ctx, q, args)
	}

	for _, b := range []struct {
		dst *[]store.LabelCount
		run func(string) ([]store.LabelCount, error)
		col string
	}{
		{&st.ByPrincipal, sessionBreakdown, "principal"},
		{&st.ByConnection, sessionBreakdown, "connection"},
		{&st.ByRisk, sessionBreakdown, "risk_level"},
		{&st.ByOperation, eventBreakdown, "operation"},
		{&st.ByRule, eventBreakdown, "rule"},
	} {
		if *b.dst, err = b.run(b.col); err != nil {
			return store.Stats{}, err
		}
	}
	return st, nil
}

func (s *Store) labelCounts(ctx context.Context, query string, args []any) ([]store.LabelCount, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("hoopinspect/store/sqlite: breakdown: %w", err)
	}
	defer rows.Close()

	var out []store.LabelCount
	for rows.Next() {
		var lc store.LabelCount
		if err := rows.Scan(&lc.Label, &lc.Count); err != nil {
			return nil, fmt.Errorf("hoopinspect/store/sqlite: scan breakdown: %w", err)
		}
		out = append(out, lc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hoopinspect/store/sqlite: breakdown rows: %w", err)
	}

	// Sorted in Go, not SQL: ties must break deterministically or a dashboard
	// reshuffles its bars between refreshes. Label ascending is the tiebreak.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > store.TopN {
		out = out[:store.TopN]
	}
	return out, nil
}

// -------------------------------------------------------------------- helpers

// micros converts a time to unix microseconds. A zero time maps to 0, which
// the schema uses as "not set" for ended_at.
func micros(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMicro()
}

func fromMicros(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMicro(v).UTC()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// encodeJSON renders a structured field, returning "" for anything empty so
// an absent value is stored as an empty string rather than the four bytes
// "null" that every reader then has to special-case.
func encodeJSON(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []string:
		if len(t) == 0 {
			return ""
		}
	case map[string]string:
		if len(t) == 0 {
			return ""
		}
	case *hoopinspect.HTTPDetail:
		if t == nil {
			return ""
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		// Every field routed here is JSON-marshalable by construction, and
		// losing one unencodable detail field beats losing the whole event.
		return ""
	}
	return string(b)
}

func decodeJSON(s string, dst any) error {
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), dst); err != nil {
		return fmt.Errorf("hoopinspect/store/sqlite: decode stored json: %w", err)
	}
	return nil
}

// likePattern lowercases the needle and escapes LIKE's wildcards, so a search
// for "100%" does not match everything.
func likePattern(s string) string {
	var b strings.Builder
	b.WriteByte('%')
	lower := strings.ToLower(s)
	for i := range len(lower) {
		switch c := lower[i]; c {
		case '%', '_', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('%')
	return b.String()
}
