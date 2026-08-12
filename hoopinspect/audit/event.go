// Package audit records what happened, durably enough to answer questions
// after the fact.
//
// # The scenario that justifies this
//
// A bad query lands on a production database. The DBA's first action is to
// disable the account that ran it. For that they need to know WHOSE identity
// it was, and every proxy that authenticates upstream with a shared service
// account makes that impossible.
//
// An Event therefore carries the principal alongside the statement.
// Everything else here follows from wanting that lookup to work at 3am.
//
// # Ordering and durability
//
// Sink implementations are free to buffer, but Write must not silently drop.
// A sink that cannot keep up returns an error and the caller decides. The
// gate treats a failed audit write as a policy failure, because an
// unrecorded statement is the one an attacker wants.
package audit

import (
	"context"
	"maps"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/session"
)

// Kind classifies an event.
type Kind string

const (
	// KindSessionStart is emitted once when a connection is accepted.
	KindSessionStart Kind = "session_start"

	// KindSessionEnd is emitted once when it closes, carrying totals.
	KindSessionEnd Kind = "session_end"

	// KindStatement is emitted for each inspected statement, with the policy
	// verdict attached. This is the row a DBA queries.
	KindStatement Kind = "statement"

	// KindViolation is emitted when a statement is denied. It duplicates the
	// denial already visible on the KindStatement row on purpose: a security
	// team should be able to select violations without scanning every
	// statement ever run.
	KindViolation Kind = "violation"

	// KindMasked is emitted when response data was rewritten, recording what
	// was masked without recording the values.
	KindMasked Kind = "masked"

	// KindError is emitted for transport or upstream failures, so a session
	// that died mid-flight is distinguishable from one that completed.
	KindError Kind = "error"
)

// Event is one audit record.
//
// Field names are snake_case and stable: they end up in JSON lines that
// someone greps a year later, and renaming one breaks their query.
type Event struct {
	// Kind classifies the record.
	Kind Kind `json:"kind"`

	// Timestamp is when it happened, UTC.
	Timestamp time.Time `json:"timestamp"`

	// SessionID ties the record to a session.
	SessionID session.ID `json:"session_id"`

	// Principal is the authenticated actor. Denormalized onto every event on
	// purpose: an audit query that needs a join to answer "who ran this" is
	// a query you will not write at 3am.
	Principal string `json:"principal"`

	// Protocol the statement came from.
	Protocol hoopinspect.Protocol `json:"protocol,omitempty"`

	// Connection is the operator-facing resource name.
	Connection string `json:"connection,omitempty"`

	// Operation is the normalized verb.
	Operation hoopinspect.Operation `json:"operation,omitempty"`

	// Statement is the text that was inspected. Subject to the sink's
	// redaction settings; see RedactStatements.
	Statement string `json:"statement,omitempty"`

	// Tables the statement referenced.
	Tables []string `json:"tables,omitempty"`

	// Allowed is the policy outcome. Only meaningful on KindStatement and
	// KindViolation.
	Allowed bool `json:"allowed"`

	// Rule identifies the policy rule that denied, when one did.
	Rule string `json:"rule,omitempty"`

	// Message is the operator-authored denial reason shown to the user.
	Message string `json:"message,omitempty"`

	// Direction says which way the bytes were travelling.
	Direction hoopinspect.Direction `json:"direction,omitempty"`

	// MaskedEntities names what was rewritten on a KindMasked event
	// (["email", "ssn"]). The VALUES stay out on purpose: an audit log that
	// records what you masked, in the clear, has un-masked it.
	MaskedEntities []string `json:"masked_entities,omitempty"`

	// MaskedCount is how many values were rewritten.
	MaskedCount int `json:"masked_count,omitempty"`

	// Error carries a transport or upstream failure.
	Error string `json:"error,omitempty"`

	// Duration is set on KindSessionEnd.
	Duration time.Duration `json:"duration_ns,omitempty"`

	// StatementCount and DeniedCount are set on KindSessionEnd.
	StatementCount int `json:"statement_count,omitempty"`
	DeniedCount    int `json:"denied_count,omitempty"`

	// HTTP carries the request/response detail for the http protocol.
	HTTP *hoopinspect.HTTPDetail `json:"http,omitempty"`

	// Metadata carries deployment-specific fields.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Sink persists events.
//
// Write is called on the connection's data path, so an implementation that
// blocks blocks the user's query. Buffer internally if the backing store is
// slow, and return an error rather than dropping when the buffer is full.
// The caller decides whether an unrecorded statement may still run.
//
// Implementations must be safe for concurrent use: one sink serves every
// connection in the process.
type Sink interface {
	Write(ctx context.Context, ev Event) error

	// Close flushes buffered events. It must be safe to call twice.
	Close() error
}

// StatementEvent builds a KindStatement (or KindViolation) event from a
// session, a statement and a verdict.
//
// allowed=false produces KindViolation, so a security team can select
// violations directly.
func StatementEvent(s *session.Session, stmt hoopinspect.Statement, allowed bool, rule, message string) Event {
	kind := KindStatement
	if !allowed {
		kind = KindViolation
	}
	return Event{
		Kind:       kind,
		Timestamp:  time.Now().UTC(),
		SessionID:  s.ID,
		Principal:  s.Identity.Principal(),
		Protocol:   stmt.Protocol,
		Connection: s.Connection,
		Operation:  stmt.Operation,
		Statement:  stmt.Text,
		Tables:     stmt.Tables,
		Direction:  stmt.Direction,
		Allowed:    allowed,
		Rule:       rule,
		Message:    message,
		HTTP:       stmt.HTTP,
		Metadata:   statementMetadata(s, stmt),
	}
}

// statementMetadata merges the session's deployment fields with the codec's
// per-statement fields.
//
// Both belong on a statement record. The session says which deployment
// produced the statement; the codec says what it could and could not read.
// `sql.incomplete` names the construct that defeated the scanner and
// `mssql.login_encrypted` marks a session whose login crossed as ciphertext,
// and without either one an operator reading the trail cannot tell a fully
// inspected statement from one the relay only partly understood.
//
// The result is always a fresh map. Handing back s.Metadata would give every
// event a reference to one shared map, and the gate writes analyzer
// annotations onto the event's copy.
func statementMetadata(s *session.Session, stmt hoopinspect.Statement) map[string]string {
	if len(s.Metadata) == 0 && len(stmt.Metadata) == 0 {
		return nil
	}
	md := make(map[string]string, len(s.Metadata)+len(stmt.Metadata))
	maps.Copy(md, s.Metadata)
	// Codec keys are namespaced (`mssql.`, `sql.`, `http.`), so a collision
	// means a deployment chose a reserved name. The codec's reading of the
	// statement in front of it is the more specific fact.
	maps.Copy(md, stmt.Metadata)
	return md
}

// SessionStartEvent builds the opening record.
func SessionStartEvent(s *session.Session) Event {
	return Event{
		Kind:       KindSessionStart,
		Timestamp:  s.StartedAt,
		SessionID:  s.ID,
		Principal:  s.Identity.Principal(),
		Protocol:   s.Protocol,
		Connection: s.Connection,
		Metadata:   s.Metadata,
	}
}

// SessionEndEvent builds the closing record with totals.
func SessionEndEvent(s *session.Session, statements, denied int) Event {
	return Event{
		Kind:           KindSessionEnd,
		Timestamp:      time.Now().UTC(),
		SessionID:      s.ID,
		Principal:      s.Identity.Principal(),
		Protocol:       s.Protocol,
		Connection:     s.Connection,
		Duration:       s.Duration(),
		StatementCount: statements,
		DeniedCount:    denied,
	}
}

// ErrorEvent builds a failure record.
func ErrorEvent(s *session.Session, err error) Event {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return Event{
		Kind:       KindError,
		Timestamp:  time.Now().UTC(),
		SessionID:  s.ID,
		Principal:  s.Identity.Principal(),
		Protocol:   s.Protocol,
		Connection: s.Connection,
		Error:      msg,
	}
}

// MaskedEvent records that response data was rewritten.
func MaskedEvent(s *session.Session, entities []string, count int) Event {
	return Event{
		Kind:           KindMasked,
		Timestamp:      time.Now().UTC(),
		SessionID:      s.ID,
		Principal:      s.Identity.Principal(),
		Protocol:       s.Protocol,
		Connection:     s.Connection,
		Direction:      hoopinspect.FromServer,
		MaskedEntities: entities,
		MaskedCount:    count,
	}
}
