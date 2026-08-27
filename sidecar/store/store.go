// Package store is the read side of the audit trail.
//
// # Separation from audit
//
// `audit.Sink` only writes. That shape fits the data path: a sink can be a
// file, a pipe, or a queue, and the gate must not care which. A UI asks
// questions a writer cannot answer: "show me alice's denied statements last
// Tuesday", "which tables did this session touch", "how many violations per
// connection this week".
//
// Answering those needs an index, and an index needs a schema. Keeping the
// query contract here means:
//
//   - The data path stays dependency-free. `audit` imports nothing; only a
//     concrete Store backend pulls in a driver.
//   - A UI can be written against Store without knowing whether the events
//     live in SQLite, Postgres, or a test fixture.
//   - A deployment that only wants JSONL never links a database at all.
//
// # The Store is also a Sink
//
// A backend implements both: it records events on the data path and serves
// queries from the same storage. One backend for both keeps the two paths
// from drifting, and an audit trail you can write but not read serves no
// one.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/session"
)

// ErrNotFound is returned when a lookup by id matches nothing. Callers should
// branch on it with errors.Is rather than treating a zero value as absence.
var ErrNotFound = errors.New("sidecar/store: not found")

// SessionRecord is a session as the read side sees it: the session facts plus
// the totals a list view needs without opening every event.
//
// The counters are denormalized on purpose. A session list is the first screen
// of any audit UI, and computing "how many statements, how many denied" with a
// correlated subquery per row makes that screen slow enough that you stop
// opening it.
type SessionRecord struct {
	ID         session.ID           `json:"id"`
	Principal  string               `json:"principal"`
	Protocol   inspect.Protocol `json:"protocol"`
	Connection string               `json:"connection,omitempty"`
	Upstream   string               `json:"upstream,omitempty"`

	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`

	// DurationMS is the session length. Zero while the session is open.
	DurationMS int64 `json:"duration_ms"`

	StatementCount int `json:"statement_count"`
	DeniedCount    int `json:"denied_count"`
	MaskedCount    int `json:"masked_count"`
	ErrorCount     int `json:"error_count"`

	// Verdict summarizes the session for a list view: "clean", "denied" when
	// any statement was refused, "error" when the transport failed.
	Verdict string `json:"verdict"`

	// RiskLevel is the highest risk seen in the session, taken from an
	// event's "risk_level" metadata key.
	//
	// Nothing in this library sets it today: risk analysis is a gateway
	// feature, and when it arrives here it will arrive as a plugin writing
	// that key. The rollup stays because such a plugin needs the seam. A
	// session's risk is the HIGHEST its statements reached, and deriving
	// that after the fact means re-reading the whole timeline.
	RiskLevel string `json:"risk_level,omitempty"`

	Metadata map[string]string `json:"metadata,omitempty"`
}

// IsOpen reports whether the session is still running.
func (r SessionRecord) IsOpen() bool { return r.EndedAt.IsZero() }

// EventRecord is one audit event with its storage id, so a UI can page
// through a session's timeline deterministically.
type EventRecord struct {
	// Seq is a monotonic per-store sequence. Timestamps collide at
	// millisecond resolution under load, so ordering and paging key on this
	// instead: two events in the same millisecond must still have a stable,
	// total order or a paging cursor silently skips rows.
	Seq int64 `json:"seq"`

	audit.Event
}

// SessionFilter narrows a session query. Zero-valued fields are ignored, so
// the zero filter lists everything.
type SessionFilter struct {
	// Principal matches the actor exactly.
	Principal string

	// Connection matches the operator-facing resource name exactly.
	Connection string

	// Protocol narrows by wire protocol.
	Protocol inspect.Protocol

	// Since and Until bound StartedAt. Until is exclusive.
	Since time.Time
	Until time.Time

	// DeniedOnly returns only sessions with at least one denial. A security
	// team runs this query daily, so it gets a first-class field rather than
	// a generic predicate.
	DeniedOnly bool

	// OpenOnly returns only sessions still running.
	OpenOnly bool

	// Search matches a substring against the principal, connection and
	// statement text. Case-insensitive. Best effort: a backend may implement
	// it as a LIKE scan, so it is slower than the indexed fields.
	Search string

	// Limit caps the result set. Zero uses DefaultLimit.
	Limit int

	// Cursor pages the result. Pass the NextCursor from a prior Page.
	Cursor string
}

// EventFilter narrows an event query within or across sessions.
type EventFilter struct {
	// SessionID restricts to one session. Usually set, since the event
	// timeline is a per-session view.
	SessionID session.ID

	// Kinds restricts to these event kinds. Empty means all.
	Kinds []audit.Kind

	// Principal, Connection, Protocol narrow across sessions.
	Principal  string
	Connection string
	Protocol   inspect.Protocol

	// DeniedOnly returns only violation events.
	DeniedOnly bool

	// Since and Until bound the event timestamp. Until is exclusive.
	Since time.Time
	Until time.Time

	// Search matches a substring against the statement text.
	Search string

	Limit  int
	Cursor string
}

// DefaultLimit bounds an unspecified page. Chosen to fill a UI table without
// a scrollbar surprise, and low enough that a runaway query cannot pull a
// million rows into memory.
const DefaultLimit = 100

// MaxLimit caps what a caller may ask for, so a hand-crafted API request
// cannot turn into an unbounded scan.
const MaxLimit = 1000

// SessionPage is a page of sessions.
type SessionPage struct {
	Sessions []SessionRecord `json:"sessions"`

	// NextCursor is empty on the last page.
	NextCursor string `json:"next_cursor,omitempty"`

	// Total is the number of sessions matching the filter, ignoring paging.
	// A backend that cannot count cheaply may report -1; a UI should then
	// render "many" rather than a wrong number.
	Total int64 `json:"total"`
}

// EventPage is a page of events.
type EventPage struct {
	Events     []EventRecord `json:"events"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Total      int64         `json:"total"`
}

// Store is the read side. A backend implements this alongside audit.Sink.
//
// Every method takes a context and must honor its cancellation: a UI that
// navigates away should not leave a scan running.
type Store interface {
	audit.Sink

	// Sessions lists sessions matching the filter, newest first.
	Sessions(ctx context.Context, f SessionFilter) (SessionPage, error)

	// Session returns one session by id, or ErrNotFound.
	Session(ctx context.Context, id session.ID) (SessionRecord, error)

	// Events lists events matching the filter, oldest first within a session
	// so a timeline reads top to bottom.
	Events(ctx context.Context, f EventFilter) (EventPage, error)

	// Stats aggregates over a window for a dashboard.
	Stats(ctx context.Context, f SessionFilter) (Stats, error)
}

// Stats is the aggregate view a dashboard renders.
type Stats struct {
	// Window describes what was aggregated.
	Since time.Time `json:"since,omitzero"`
	Until time.Time `json:"until,omitzero"`

	Sessions   int64 `json:"sessions"`
	Statements int64 `json:"statements"`
	Denied     int64 `json:"denied"`
	Masked     int64 `json:"masked"`
	Errors     int64 `json:"errors"`

	// ByPrincipal, ByConnection, ByOperation and ByRule are the breakdowns a
	// dashboard charts. Each maps a label to a count, sorted by the backend
	// into descending order before truncation to TopN.
	ByPrincipal  []LabelCount `json:"by_principal,omitempty"`
	ByConnection []LabelCount `json:"by_connection,omitempty"`
	ByOperation  []LabelCount `json:"by_operation,omitempty"`
	ByRule       []LabelCount `json:"by_rule,omitempty"`

	// ByRisk counts AI-analysis verdicts by risk level.
	ByRisk []LabelCount `json:"by_risk,omitempty"`
}

// LabelCount is one bar in a breakdown.
type LabelCount struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// TopN bounds a breakdown. A dashboard cannot render a thousand bars, so a
// query that returns them all pays scan cost for rows the UI drops.
const TopN = 20

// Normalize clamps a filter's paging to the allowed range. Backends should
// call it before building a query so limits are enforced in one place rather
// than per backend.
func (f SessionFilter) Normalize() SessionFilter {
	if f.Limit <= 0 {
		f.Limit = DefaultLimit
	}
	if f.Limit > MaxLimit {
		f.Limit = MaxLimit
	}
	return f
}

// Normalize clamps an event filter's paging.
func (f EventFilter) Normalize() EventFilter {
	if f.Limit <= 0 {
		f.Limit = DefaultLimit
	}
	if f.Limit > MaxLimit {
		f.Limit = MaxLimit
	}
	return f
}

// Verdict classifications for SessionRecord.Verdict.
const (
	VerdictClean  = "clean"
	VerdictDenied = "denied"
	VerdictError  = "error"
)

// ClassifyVerdict derives the session verdict from its counters. Denials
// outrank errors: a session that was refused AND then failed matters first
// for the refusal.
func ClassifyVerdict(denied, errors int) string {
	switch {
	case denied > 0:
		return VerdictDenied
	case errors > 0:
		return VerdictError
	}
	return VerdictClean
}
