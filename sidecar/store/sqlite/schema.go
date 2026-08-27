package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaVersion is the version this build writes. It gives a future
// migration somewhere to stand: an older binary opening a newer file can
// refuse rather than misread columns it does not know about.
const schemaVersion = 1

// schemaDDL is applied at every open. Every statement is idempotent, so
// opening an existing database is a no-op rather than a migration.
//
// Timestamps are stored as INTEGER unix microseconds. Text timestamps sort
// correctly only if every writer agrees on the exact format and zone; an
// integer sorts correctly by construction. That matters because the session
// cursor pages on (started_at, id), and a mis-sorting key silently skips
// rows. Microseconds because millisecond resolution collides under load and
// nanoseconds overflow the useful range of int64 arithmetic in SQLite's date
// functions.
//
// List-valued and structured fields (tables, masked_entities, http, metadata)
// are JSON text. They carry display and detail data, never join keys: a
// normalized tables table would add an index no query uses and cost a second
// write per statement on the connection's data path.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	id              TEXT PRIMARY KEY,
	principal       TEXT NOT NULL DEFAULT '',
	protocol        TEXT NOT NULL DEFAULT '',
	connection      TEXT NOT NULL DEFAULT '',
	upstream        TEXT NOT NULL DEFAULT '',
	started_at      INTEGER NOT NULL,
	ended_at        INTEGER NOT NULL DEFAULT 0,
	duration_ms     INTEGER NOT NULL DEFAULT 0,
	statement_count INTEGER NOT NULL DEFAULT 0,
	denied_count    INTEGER NOT NULL DEFAULT 0,
	masked_count    INTEGER NOT NULL DEFAULT 0,
	error_count     INTEGER NOT NULL DEFAULT 0,
	verdict         TEXT NOT NULL DEFAULT '',
	risk_level      TEXT NOT NULL DEFAULT '',
	metadata        TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS events (
	seq             INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id      TEXT NOT NULL,
	kind            TEXT NOT NULL,
	timestamp       INTEGER NOT NULL,
	principal       TEXT NOT NULL DEFAULT '',
	protocol        TEXT NOT NULL DEFAULT '',
	connection      TEXT NOT NULL DEFAULT '',
	operation       TEXT NOT NULL DEFAULT '',
	statement       TEXT NOT NULL DEFAULT '',
	tables          TEXT NOT NULL DEFAULT '',
	allowed         INTEGER NOT NULL DEFAULT 0,
	rule            TEXT NOT NULL DEFAULT '',
	message         TEXT NOT NULL DEFAULT '',
	direction       TEXT NOT NULL DEFAULT '',
	masked_entities TEXT NOT NULL DEFAULT '',
	masked_count    INTEGER NOT NULL DEFAULT 0,
	error           TEXT NOT NULL DEFAULT '',
	-- Set only on session_end. Kept on the event so a timeline row
	-- round-trips the audit.Event it came from rather than losing its totals.
	duration_ns     INTEGER NOT NULL DEFAULT 0,
	statement_count INTEGER NOT NULL DEFAULT 0,
	denied_count    INTEGER NOT NULL DEFAULT 0,
	http            TEXT NOT NULL DEFAULT '',
	metadata        TEXT NOT NULL DEFAULT ''
);

-- Every index below backs a field SessionFilter or EventFilter narrows on, or
-- a paging key. An index that backs neither is write amplification on the
-- connection's data path, so there are no others.

-- The unfiltered session list is the default screen and pages on
-- (started_at DESC, id DESC); without this index that is a full scan plus a
-- sort on every page.
CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at DESC, id DESC);

-- SessionFilter.Principal: "what did alice do", the query run during an
-- incident. Composite with the paging key so the filtered list is still an
-- index range scan rather than a scan-then-sort.
CREATE INDEX IF NOT EXISTS idx_sessions_principal ON sessions(principal, started_at DESC, id DESC);

-- SessionFilter.Connection: the per-resource view a database owner opens.
CREATE INDEX IF NOT EXISTS idx_sessions_connection ON sessions(connection, started_at DESC, id DESC);

-- SessionFilter.Protocol: low cardinality, but it combines with a time range
-- and the composite still lets SQLite range-scan instead of sorting.
CREATE INDEX IF NOT EXISTS idx_sessions_protocol ON sessions(protocol, started_at DESC, id DESC);

-- SessionFilter.DeniedOnly. Partial: denied sessions are the rare ones, so
-- indexing only them keeps the index small and makes the security team's
-- standing query a scan of exactly the rows it wants.
CREATE INDEX IF NOT EXISTS idx_sessions_denied ON sessions(started_at DESC, id DESC) WHERE denied_count > 0;

-- SessionFilter.OpenOnly. Partial for the same reason: in a healthy system
-- almost every row is closed, so the live-session view should not read them.
CREATE INDEX IF NOT EXISTS idx_sessions_open ON sessions(started_at DESC, id DESC) WHERE ended_at = 0;

-- Stats.ByRisk groups on risk_level, and only analyzed sessions have one.
CREATE INDEX IF NOT EXISTS idx_sessions_risk ON sessions(risk_level) WHERE risk_level <> '';

-- EventFilter.SessionID with seq paging: the per-session timeline, the single
-- most common event query. seq is the cursor key, so it belongs in the index.
CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id, seq);

-- EventFilter.Kinds, usually combined with a session. Kind first because it
-- is the selective term for the cross-session "show me every violation" view.
CREATE INDEX IF NOT EXISTS idx_events_kind ON events(kind, seq);

-- EventFilter.Since/Until across sessions, and the Stats window.
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);

-- EventFilter.Principal / Connection / Protocol, the cross-session narrowing
-- fields. seq trails each so paging stays a range scan.
CREATE INDEX IF NOT EXISTS idx_events_principal ON events(principal, seq);
CREATE INDEX IF NOT EXISTS idx_events_connection ON events(connection, seq);
CREATE INDEX IF NOT EXISTS idx_events_protocol ON events(protocol, seq);

-- Stats.ByOperation and ByRule group on these. Partial on rule because only
-- denials carry one and an index full of '' answers no question.
CREATE INDEX IF NOT EXISTS idx_events_operation ON events(operation) WHERE operation <> '';
CREATE INDEX IF NOT EXISTS idx_events_rule ON events(rule) WHERE rule <> '';
`

// applySchema creates the tables and records the version. Safe to call on an
// existing database.
func applySchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("sidecar/store/sqlite: apply schema: %w", err)
	}

	var found int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&found)
	if err != nil {
		return fmt.Errorf("sidecar/store/sqlite: read schema version: %w", err)
	}
	switch {
	case found == 0:
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES (?)`, schemaVersion); err != nil {
			return fmt.Errorf("sidecar/store/sqlite: record schema version: %w", err)
		}
	case found > schemaVersion:
		// Refuse rather than misread. A newer writer may have repurposed a
		// column; an error at open beats serving wrong audit data.
		return fmt.Errorf("sidecar/store/sqlite: database schema version %d is newer than supported %d", found, schemaVersion)
	}
	return nil
}
