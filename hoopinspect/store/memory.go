package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoopinspect/audit"
	"github.com/hoophq/hoopinspect/session"
)

// MemoryStore is an in-process Store.
//
// # What it is for
//
// Three real uses, in order of importance:
//
//  1. The default backend of the sidecar's query API, so `/sessions` works
//     out of the box with no database to provision. A team evaluating this
//     gets a working audit UI backend on first run.
//  2. Testing anything that consumes a Store, without a driver.
//  3. A bounded live window in front of a durable sink, so the recent-events
//     view is fast while the JSONL file remains the record of truth.
//
// # What it is not
//
// Not durable. It is a bounded ring: when full it evicts the OLDEST session
// and every event belonging to it. A deployment that needs a complete trail
// pairs it with a JSONL or SQLite sink via audit.MultiSink — and the API
// reports what was dropped so a reader can tell the window is partial rather
// than silently believing they see everything.
type MemoryStore struct {
	mu sync.RWMutex

	// maxSessions bounds retention. Eviction is by session rather than by
	// event so a session's timeline is never half-present, which would make
	// the detail view lie.
	maxSessions int

	sessions map[session.ID]*SessionRecord

	// order tracks insertion order for eviction, oldest first.
	order []session.ID

	// events holds every retained event, in arrival order. seq is the index
	// into this slice plus one, so paging is a slice operation.
	events  []EventRecord
	nextSeq int64

	// droppedSessions counts evictions, so the API can say the window is
	// incomplete instead of implying it is whole.
	droppedSessions int64

	closed bool
}

// DefaultMemorySessions bounds a MemoryStore created with 0. Sized so a busy
// sidecar keeps roughly a day of sessions in a few tens of MB.
const DefaultMemorySessions = 1000

// NewMemoryStore returns a bounded in-memory Store.
func NewMemoryStore(maxSessions int) *MemoryStore {
	if maxSessions <= 0 {
		maxSessions = DefaultMemorySessions
	}
	return &MemoryStore{
		maxSessions: maxSessions,
		sessions:    make(map[session.ID]*SessionRecord, maxSessions),
	}
}

// Write implements audit.Sink. It records the event and maintains the
// denormalized session counters.
func (m *MemoryStore) Write(_ context.Context, ev audit.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return errors.New("hoopinspect/store: memory store closed")
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}

	rec := m.sessions[ev.SessionID]
	if rec == nil {
		// An event may arrive with no preceding session_start when a sink is
		// attached mid-session. Creating the row from whatever the event
		// carries beats dropping the event.
		rec = &SessionRecord{
			ID:         ev.SessionID,
			Principal:  ev.Principal,
			Protocol:   ev.Protocol,
			Connection: ev.Connection,
			StartedAt:  ev.Timestamp,
			Metadata:   ev.Metadata,
		}
		m.sessions[ev.SessionID] = rec
		m.order = append(m.order, ev.SessionID)
		m.evictLocked()
	}

	applyEvent(rec, ev)

	m.nextSeq++
	m.events = append(m.events, EventRecord{Seq: m.nextSeq, Event: ev})
	return nil
}

// applyEvent folds one event into a session record's counters. Shared with
// any other backend that needs the same denormalization semantics.
func applyEvent(rec *SessionRecord, ev audit.Event) {
	switch ev.Kind {
	case audit.KindSessionStart:
		rec.StartedAt = ev.Timestamp
		if ev.Metadata != nil {
			rec.Metadata = ev.Metadata
		}
	case audit.KindSessionEnd:
		rec.EndedAt = ev.Timestamp
		rec.DurationMS = ev.Duration.Milliseconds()
		// Trust the end event's totals over the running counts: it is
		// authoritative, and a sink attached mid-session may have missed
		// earlier statements.
		if ev.StatementCount > 0 {
			rec.StatementCount = ev.StatementCount
		}
		if ev.DeniedCount > 0 {
			rec.DeniedCount = ev.DeniedCount
		}
	case audit.KindStatement:
		rec.StatementCount++
	case audit.KindViolation:
		rec.StatementCount++
		rec.DeniedCount++
	case audit.KindMasked:
		rec.MaskedCount += ev.MaskedCount
	case audit.KindError:
		rec.ErrorCount++
	}

	if rec.Principal == "" {
		rec.Principal = ev.Principal
	}
	if rec.Protocol == "" {
		rec.Protocol = ev.Protocol
	}
	if rec.Connection == "" {
		rec.Connection = ev.Connection
	}
	if risk := ev.Metadata["risk_level"]; risk != "" && riskRank(risk) > riskRank(rec.RiskLevel) {
		// Highest risk wins: an average would let one dangerous statement
		// hide behind fifty harmless ones.
		rec.RiskLevel = risk
	}
	rec.Verdict = ClassifyVerdict(rec.DeniedCount, rec.ErrorCount)
}

func riskRank(level string) int {
	switch strings.ToLower(level) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

// evictLocked drops oldest sessions until the store is within its bound.
func (m *MemoryStore) evictLocked() {
	for len(m.order) > m.maxSessions {
		victim := m.order[0]
		m.order = m.order[1:]
		delete(m.sessions, victim)
		m.droppedSessions++

		// Drop the victim's events too, so a session is never half-present.
		kept := m.events[:0]
		for _, e := range m.events {
			if e.SessionID != victim {
				kept = append(kept, e)
			}
		}
		m.events = kept
	}
}

// Close implements audit.Sink. The retained data stays readable so a
// shutdown path can still serve a final query.
func (m *MemoryStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// Dropped reports how many sessions were evicted. A reader that ignores this
// believes a partial window is complete.
func (m *MemoryStore) Dropped() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.droppedSessions
}

// Session implements Store.
func (m *MemoryStore) Session(_ context.Context, id session.ID) (SessionRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.sessions[id]
	if !ok {
		return SessionRecord{}, fmt.Errorf("%w: session %q", ErrNotFound, id)
	}
	return *rec, nil
}

// Sessions implements Store, newest first.
func (m *MemoryStore) Sessions(ctx context.Context, f SessionFilter) (SessionPage, error) {
	if err := ctx.Err(); err != nil {
		return SessionPage{}, err
	}
	f = f.Normalize()

	m.mu.RLock()
	matched := make([]SessionRecord, 0, len(m.sessions))
	for _, rec := range m.sessions {
		if matchSession(*rec, f) {
			matched = append(matched, *rec)
		}
	}
	m.mu.RUnlock()

	// Newest first, with the id as a tiebreaker so the order is total.
	// Without the tiebreaker two sessions started in the same millisecond
	// could swap places between pages and a cursor would skip one.
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].StartedAt.Equal(matched[j].StartedAt) {
			return matched[i].StartedAt.After(matched[j].StartedAt)
		}
		return matched[i].ID > matched[j].ID
	})

	total := int64(len(matched))

	start := 0
	if f.Cursor != "" {
		c, err := decodeCursor(f.Cursor)
		if err != nil {
			return SessionPage{}, err
		}
		// Keyset, not offset: resume after the last id seen, so rows
		// inserted since the previous page cannot shift the window.
		for i, rec := range matched {
			if string(rec.ID) == c.ID {
				start = i + 1
				break
			}
		}
	}

	end := min(start+f.Limit, len(matched))
	if start > len(matched) {
		start = len(matched)
	}
	page := SessionPage{Sessions: matched[start:end], Total: total}
	if end < len(matched) && end > start {
		page.NextCursor = encodeCursor(cursor{ID: string(matched[end-1].ID)})
	}
	return page, nil
}

// Events implements Store, oldest first so a timeline reads top to bottom.
func (m *MemoryStore) Events(ctx context.Context, f EventFilter) (EventPage, error) {
	if err := ctx.Err(); err != nil {
		return EventPage{}, err
	}
	f = f.Normalize()

	var after int64
	if f.Cursor != "" {
		c, err := decodeCursor(f.Cursor)
		if err != nil {
			return EventPage{}, err
		}
		after = c.Seq
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	matched := make([]EventRecord, 0, f.Limit)
	var total int64
	for _, e := range m.events {
		if !matchEvent(e, f) {
			continue
		}
		total++
		if e.Seq <= after {
			continue
		}
		if len(matched) < f.Limit {
			matched = append(matched, e)
		}
	}

	page := EventPage{Events: matched, Total: total}
	if len(matched) == f.Limit && int64(len(matched)) < total {
		page.NextCursor = encodeCursor(cursor{Seq: matched[len(matched)-1].Seq})
	}
	return page, nil
}

// Stats implements Store.
func (m *MemoryStore) Stats(ctx context.Context, f SessionFilter) (Stats, error) {
	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	out := Stats{Since: f.Since, Until: f.Until}
	byPrincipal := map[string]int64{}
	byConnection := map[string]int64{}
	byRisk := map[string]int64{}

	live := map[session.ID]bool{}
	for _, rec := range m.sessions {
		if !matchSession(*rec, f) {
			continue
		}
		live[rec.ID] = true
		out.Sessions++
		out.Statements += int64(rec.StatementCount)
		out.Denied += int64(rec.DeniedCount)
		out.Masked += int64(rec.MaskedCount)
		out.Errors += int64(rec.ErrorCount)

		if rec.Principal != "" {
			byPrincipal[rec.Principal]++
		}
		if rec.Connection != "" {
			byConnection[rec.Connection]++
		}
		if rec.RiskLevel != "" {
			byRisk[rec.RiskLevel]++
		}
	}

	byOperation := map[string]int64{}
	byRule := map[string]int64{}
	for _, e := range m.events {
		if !live[e.SessionID] {
			continue
		}
		if e.Operation != "" {
			byOperation[string(e.Operation)]++
		}
		if e.Rule != "" {
			byRule[e.Rule]++
		}
	}

	out.ByPrincipal = topN(byPrincipal)
	out.ByConnection = topN(byConnection)
	out.ByOperation = topN(byOperation)
	out.ByRule = topN(byRule)
	out.ByRisk = topN(byRisk)
	return out, nil
}

// topN sorts a breakdown descending and truncates it. A dashboard cannot
// render a thousand bars, and a query returning them all got slow for no
// benefit.
func topN(counts map[string]int64) []LabelCount {
	if len(counts) == 0 {
		return nil
	}
	out := make([]LabelCount, 0, len(counts))
	for label, n := range counts {
		out = append(out, LabelCount{Label: label, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		// Alphabetical tiebreak keeps the output stable across calls, so a
		// dashboard's bars do not reshuffle on refresh.
		return out[i].Label < out[j].Label
	})
	if len(out) > TopN {
		out = out[:TopN]
	}
	return out
}

func matchSession(rec SessionRecord, f SessionFilter) bool {
	if f.Principal != "" && rec.Principal != f.Principal {
		return false
	}
	if f.Connection != "" && rec.Connection != f.Connection {
		return false
	}
	if f.Protocol != "" && rec.Protocol != f.Protocol {
		return false
	}
	if f.DeniedOnly && rec.DeniedCount == 0 {
		return false
	}
	if f.OpenOnly && !rec.IsOpen() {
		return false
	}
	if !f.Since.IsZero() && rec.StartedAt.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !rec.StartedAt.Before(f.Until) {
		return false
	}
	if f.Search != "" {
		needle := strings.ToLower(f.Search)
		hay := strings.ToLower(rec.Principal + " " + rec.Connection + " " + string(rec.Protocol))
		if !strings.Contains(hay, needle) {
			return false
		}
	}
	return true
}

func matchEvent(e EventRecord, f EventFilter) bool {
	if f.SessionID != "" && e.SessionID != f.SessionID {
		return false
	}
	if f.Principal != "" && e.Principal != f.Principal {
		return false
	}
	if f.Connection != "" && e.Connection != f.Connection {
		return false
	}
	if f.Protocol != "" && e.Protocol != f.Protocol {
		return false
	}
	if f.DeniedOnly && e.Kind != audit.KindViolation {
		return false
	}
	if len(f.Kinds) > 0 {
		var ok bool
		for _, k := range f.Kinds {
			if e.Kind == k {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if !f.Since.IsZero() && e.Timestamp.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !e.Timestamp.Before(f.Until) {
		return false
	}
	if f.Search != "" && !strings.Contains(strings.ToLower(e.Statement), strings.ToLower(f.Search)) {
		return false
	}
	return true
}

// cursor is the opaque paging token. Keyset rather than offset: on a live
// audit trail rows arrive constantly, and an OFFSET page silently skips or
// repeats rows when that happens.
type cursor struct {
	// ID resumes a session listing after this session.
	ID string `json:"id,omitempty"`
	// Seq resumes an event listing after this sequence number.
	Seq int64 `json:"seq,omitempty"`
}

func encodeCursor(c cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor rejects a malformed token rather than silently restarting from
// page one, which would make a paging bug look like duplicated data.
func decodeCursor(s string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, errors.New("hoopinspect/store: malformed cursor")
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return cursor{}, errors.New("hoopinspect/store: malformed cursor")
	}
	return c, nil
}

// compile-time proof the backend satisfies the contract.
var _ Store = (*MemoryStore)(nil)
