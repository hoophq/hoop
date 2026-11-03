package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// MemoryStore is an in-process Store.
//
// # Uses
//
// Three, in order of importance:
//
//  1. The default backend of the sidecar's query API, so `/sessions` works
//     out of the box with no database to provision. A team evaluating this
//     gets a working audit UI backend on first run.
//  2. Testing anything that consumes a Store, without a driver.
//  3. A bounded live window in front of a durable sink, so the recent-events
//     view is fast while the JSONL file remains the record of truth.
//
// # Boundaries
//
// The store keeps nothing across a restart. Two bounds hold it, and either
// one evicts:
//
//   - MaxSessions: a new session past the bound evicts the OLDEST session
//     and every event belonging to it.
//   - MaxBytes: events past the byte budget are dropped oldest first, from
//     whichever session holds them. A session still retained that lost an
//     event reports it in SessionRecord.EventsDropped, so its timeline is
//     never shown as whole when it is not. Its counters stay exact.
//
// The byte budget is what bounds a long-lived session: a connection pool or
// an agent keeps a few sessions open for days, and a session count alone
// never evicts them.
//
// For a complete trail, pair it with a JSONL or SQLite sink via
// audit.MultiSink.
type MemoryStore struct {
	mu sync.RWMutex

	maxSessions int
	maxBytes    int64

	// opts is the policy the JSONL sink applies, so the store never holds a
	// statement the file redacted, or a longer one than the file kept.
	opts audit.SinkOptions

	sessions map[session.ID]*memSession

	// order tracks session insertion order for eviction, oldest first.
	order []session.ID

	// oldest and newest hold every retained event in arrival order. A list,
	// not a slice: dropping one session's events from the middle is O(1)
	// per event, and a dropped event is freed at once instead of pinning
	// the capacity of a slice that once held millions.
	oldest, newest *memEvent

	// bytes is the estimated size of the retained events; see eventSize.
	bytes   int64
	nextSeq int64

	// droppedSessions counts session evictions, so a reader can tell the
	// window is incomplete instead of implying it is whole.
	droppedSessions int64

	closed bool
}

// memEvent is one retained event with its two list links.
type memEvent struct {
	EventRecord
	size int64

	// prev and next link every retained event in arrival order.
	prev, next *memEvent
	// sessionNext links the events of one session in arrival order.
	sessionNext *memEvent
}

// memSession is a session's record and its retained timeline. first is
// always the session's oldest retained event, and so the first of them the
// byte budget drops.
type memSession struct {
	rec         SessionRecord
	first, last *memEvent
}

// MemoryOptions configures a MemoryStore. The zero value is usable.
type MemoryOptions struct {
	// MaxSessions bounds how many sessions are retained. Zero or less uses
	// DefaultMemorySessions.
	MaxSessions int

	// MaxBytes bounds the estimated size of the retained events. Zero or
	// less uses DefaultMemoryBytes. The newest event is always kept, so one
	// event larger than the budget holds the store alone.
	MaxBytes int64

	// Sink is applied to each event before it is stored: redaction, the
	// statement cap and timestamp normalization, as audit.JSONLSink does.
	Sink audit.SinkOptions
}

// DefaultMemorySessions bounds a MemoryStore created with no MaxSessions.
const DefaultMemorySessions = 1000

// DefaultMemoryBytes bounds a MemoryStore created with no MaxBytes. At the
// default 8 KiB statement cap that is at least eight thousand events, and
// tens of thousands of typical ones.
const DefaultMemoryBytes int64 = 64 << 20

// NewMemoryStore returns a bounded in-memory Store.
func NewMemoryStore(o MemoryOptions) *MemoryStore {
	if o.MaxSessions <= 0 {
		o.MaxSessions = DefaultMemorySessions
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMemoryBytes
	}
	return &MemoryStore{
		maxSessions: o.MaxSessions,
		maxBytes:    o.MaxBytes,
		opts:        o.Sink,
		sessions:    make(map[session.ID]*memSession),
	}
}

// Write implements audit.Sink. It records the event and maintains the
// denormalized session counters.
func (m *MemoryStore) Write(_ context.Context, ev audit.Event) error {
	// Outside the lock: the policy the file applies, then a copy of every
	// string into memory the store owns. A statement's table names and text
	// can be slices of a larger buffer, and holding them would hold all of it
	// while the budget counts a few bytes.
	ev, content := own(m.opts.Apply(ev))
	size := eventSize(&ev, content)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return errors.New("sidecar/store: memory store closed")
	}

	s := m.sessions[ev.SessionID]
	if s == nil {
		// An event may arrive with no preceding session_start when a sink is
		// attached mid-session. applyEvent builds the row from whatever the
		// event carries rather than dropping it. The id is the record's own,
		// so the map key does not pin this event.
		id := session.ID(strings.Clone(string(ev.SessionID)))
		s = &memSession{rec: SessionRecord{ID: id, StartedAt: ev.Timestamp}}
		m.sessions[id] = s
		m.order = append(m.order, id)
		m.evictSessionsLocked()
	}

	before := s.rec
	applyEvent(&s.rec, ev)
	ownRecord(&s.rec, &before)

	m.nextSeq++
	e := &memEvent{EventRecord: EventRecord{Seq: m.nextSeq, Event: ev}, size: size}
	if m.newest == nil {
		m.oldest = e
	} else {
		m.newest.next = e
		e.prev = m.newest
	}
	m.newest = e
	if s.last == nil {
		s.first = e
	} else {
		s.last.sessionNext = e
	}
	s.last = e
	m.bytes += e.size

	for m.bytes > m.maxBytes && m.oldest != e {
		m.dropOldestLocked()
	}
	return nil
}

// applyEvent folds one event into a session record's counters. Shared with
// any other backend that needs the same denormalization semantics.
func applyEvent(rec *SessionRecord, ev audit.Event) {
	switch ev.Kind {
	case audit.KindSessionStart:
		rec.StartedAt = ev.Timestamp
		if ev.Metadata != nil {
			rec.Metadata = maps.Clone(ev.Metadata)
		}
	case audit.KindSessionEnd:
		rec.EndedAt = ev.Timestamp
		rec.DurationMS = ev.Duration.Milliseconds()
		// The row's metadata is the SESSION's, so it comes from the two
		// records that carry nothing else: session_start and session_end.
		// A statement's metadata mixes in codec keys (pg.message) and
		// verdict annotations that describe one statement, not the
		// session. session_end supersedes session_start because a lane can
		// learn session facts after the start is written: a pgwire lane
		// reads its StartupMessage only then.
		if ev.Metadata != nil {
			rec.Metadata = maps.Clone(ev.Metadata)
		}
		// Trust the end event's totals over the running counts. The end
		// event is authoritative, and a sink attached mid-session may have
		// missed earlier statements.
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

	// "anonymous" is a placeholder, not a value: Identity.Principal() returns
	// it whenever no subject is set, so it is never the empty string this
	// check would otherwise look for. Treat it as unset, or a session whose
	// actor becomes known only after session_start keeps reporting nobody.
	//
	// That is the ordinary case on a postgres lane, where the relay learns the
	// user from the StartupMessage after the session has already opened.
	if rec.Principal == "" || rec.Principal == session.AnonymousPrincipal {
		rec.Principal = ev.Principal
	}
	if rec.Protocol == "" {
		rec.Protocol = ev.Protocol
	}
	if rec.Connection == "" {
		rec.Connection = ev.Connection
	}
	if risk := ev.Metadata["risk_level"]; risk != "" && riskRank(risk) > riskRank(rec.RiskLevel) {
		// Highest risk wins. An average would let one dangerous statement
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

// evictSessionsLocked drops oldest sessions until the store is within its
// session bound. Each eviction walks only the victim's own events.
func (m *MemoryStore) evictSessionsLocked() {
	for len(m.order) > m.maxSessions {
		victim := m.order[0]
		m.order = m.order[1:]
		s := m.sessions[victim]
		delete(m.sessions, victim)
		m.droppedSessions++

		// Drop the victim's events too, so a session is never half-present.
		for e := s.first; e != nil; {
			next := e.sessionNext
			m.unlinkLocked(e)
			e = next
		}
	}
}

// dropOldestLocked drops the oldest retained event and marks its session's
// timeline as incomplete. The oldest event overall is the oldest of its
// session, so it is always that session's first.
func (m *MemoryStore) dropOldestLocked() {
	e := m.oldest
	s := m.sessions[e.SessionID]
	s.first = e.sessionNext
	if s.first == nil {
		s.last = nil
	}
	s.rec.EventsDropped++
	m.unlinkLocked(e)
}

// unlinkLocked removes e from the arrival-order list and releases its bytes.
// The caller owns the session list.
func (m *MemoryStore) unlinkLocked(e *memEvent) {
	if e.prev == nil {
		m.oldest = e.next
	} else {
		e.prev.next = e.next
	}
	if e.next == nil {
		m.newest = e.prev
	} else {
		e.next.prev = e.prev
	}
	e.prev, e.next, e.sessionNext = nil, nil, nil
	m.bytes -= e.size
}

// memEventOverhead is the fixed part of a retained event: the struct with
// its links, the session map and order slots are not counted.
const memEventOverhead = int64(unsafe.Sizeof(memEvent{}))

// stringHeader is the size of a string or a slice element pointing to one.
const stringHeader = int64(unsafe.Sizeof(""))

// mapEntryOverhead approximates what a Go map spends per entry beyond the
// key and value bytes: their headers and the slot's share of a group.
const mapEntryOverhead = 3 * stringHeader

// mapBaseOverhead is what a non-empty map costs before its first entry: the
// map header and one group of eight slots, which even a one-entry map
// allocates whole.
const mapBaseOverhead = 48 + 8*(1+2*stringHeader)

// own returns a copy of ev whose every string lives in one allocation of
// content bytes, and whose slices, maps and HTTP detail are its own. ev and
// everything it references stay untouched: the caller and the other sinks
// still hold them. TestOwnDetachesEveryString fails when a field is added
// to audit.Event or HTTPDetail without a line in ownStrings.
func own(ev audit.Event) (_ audit.Event, content int) {
	var a arena
	ownStrings(&a, &ev) // measures, and writes nothing
	a.buf = make([]byte, 0, a.n)
	a.copying = true
	ownStrings(&a, &ev)
	return ev, a.n
}

// arena measures strings on the first pass and copies them into buf on the
// second. buf is sized by the first pass, so it never moves while strings
// point into it.
type arena struct {
	n       int
	copying bool
	buf     []byte
}

func (a *arena) str(s string) string {
	if !a.copying {
		a.n += len(s)
		return s
	}
	if s == "" {
		return ""
	}
	off := len(a.buf)
	a.buf = append(a.buf, s...)
	return unsafe.String(&a.buf[off], len(s))
}

func setStr[S ~string](a *arena, p *S) {
	if s := a.str(string(*p)); a.copying {
		*p = S(s)
	}
}

func setStrs(a *arena, p *[]string) {
	if !a.copying {
		for _, s := range *p {
			a.str(s)
		}
		return
	}
	if *p == nil {
		return
	}
	out := make([]string, len(*p))
	for i, s := range *p {
		out[i] = a.str(s)
	}
	*p = out
}

func setStrMap(a *arena, p *map[string]string) {
	if !a.copying {
		for k, v := range *p {
			a.str(k)
			a.str(v)
		}
		return
	}
	if *p == nil {
		return
	}
	out := make(map[string]string, len(*p))
	for k, v := range *p {
		out[a.str(k)] = a.str(v)
	}
	*p = out
}

// ownStrings visits every string ev references. On the copying pass it
// replaces each container before writing into it, so nothing the caller
// shares is written.
func ownStrings(a *arena, ev *audit.Event) {
	setStr(a, &ev.Kind)
	setStr(a, &ev.SessionID)
	setStr(a, &ev.Principal)
	setStr(a, &ev.Protocol)
	setStr(a, &ev.Connection)
	setStr(a, &ev.Operation)
	setStr(a, &ev.Statement)
	setStr(a, &ev.Rule)
	setStr(a, &ev.Message)
	setStr(a, &ev.Direction)
	setStr(a, &ev.Error)
	setStrs(a, &ev.Tables)
	setStrs(a, &ev.MaskedEntities)
	setStrMap(a, &ev.Metadata)

	if ev.HTTP == nil {
		return
	}
	h := ev.HTTP
	if a.copying {
		c := *h
		h, ev.HTTP = &c, &c
	}
	setStr(a, &h.Method)
	setStr(a, &h.Path)
	setStr(a, &h.Target)
	setStr(a, &h.Host)
	setStr(a, &h.Resource)
	setStr(a, &h.ContentType)
	setStr(a, &h.Body)
	setStrMap(a, &h.Headers)
	if a.copying && h.Query != nil {
		q := make(map[string][]string, len(h.Query))
		for k, vs := range h.Query {
			setStrs(a, &vs)
			q[a.str(k)] = vs
		}
		h.Query = q
	} else if !a.copying {
		for k, vs := range h.Query {
			a.str(k)
			setStrs(a, &vs)
		}
	}

	if h.WebSocket == nil {
		return
	}
	w := h.WebSocket
	if a.copying {
		c := *w
		w, h.WebSocket = &c, &c
	}
	setStr(a, &w.Opcode)
	setStr(a, &w.Subprotocol)
	setStr(a, &w.CloseReason)
}

// ownRecord copies each string or map applyEvent just took from an event,
// so a session record does not pin the event once the budget drops it.
func ownRecord(rec, before *SessionRecord) {
	keep := func(s, old string) string {
		if len(s) == len(old) && unsafe.StringData(s) == unsafe.StringData(old) {
			return s
		}
		return strings.Clone(s)
	}
	rec.Principal = keep(rec.Principal, before.Principal)
	rec.Protocol = inspect.Protocol(keep(string(rec.Protocol), string(before.Protocol)))
	rec.Connection = keep(rec.Connection, before.Connection)
	rec.RiskLevel = keep(rec.RiskLevel, before.RiskLevel)
	if reflect.ValueOf(rec.Metadata).UnsafePointer() != reflect.ValueOf(before.Metadata).UnsafePointer() {
		md := make(map[string]string, len(rec.Metadata))
		for k, v := range rec.Metadata {
			md[strings.Clone(k)] = strings.Clone(v)
		}
		rec.Metadata = md
	}
}

// eventSize estimates the heap an owned event holds: the fixed struct, its
// content bytes and the slices and maps around them.
func eventSize(ev *audit.Event, content int) int64 {
	n := memEventOverhead + int64(content)
	n += int64(cap(ev.Tables)+cap(ev.MaskedEntities)) * stringHeader
	n += mapSize(len(ev.Metadata))
	if h := ev.HTTP; h != nil {
		n += int64(unsafe.Sizeof(*h)) + mapSize(len(h.Headers)) + mapSize(len(h.Query))
		for _, vs := range h.Query {
			n += int64(cap(vs)) * stringHeader
		}
		if w := h.WebSocket; w != nil {
			n += int64(unsafe.Sizeof(*w))
		}
	}
	return n
}

func mapSize(entries int) int64 {
	if entries == 0 {
		return 0
	}
	return mapBaseOverhead + int64(entries)*mapEntryOverhead
}

// Close implements audit.Sink. The retained data stays readable so a
// shutdown path can still serve a final query.
func (m *MemoryStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// Dropped reports how many sessions were evicted. Check it before you read a
// window as complete.
func (m *MemoryStore) Dropped() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.droppedSessions
}

// Session implements Store.
func (m *MemoryStore) Session(_ context.Context, id session.ID) (SessionRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok {
		return SessionRecord{}, fmt.Errorf("%w: session %q", ErrNotFound, id)
	}
	return s.rec, nil
}

// Sessions implements Store, newest first.
func (m *MemoryStore) Sessions(ctx context.Context, f SessionFilter) (SessionPage, error) {
	if err := ctx.Err(); err != nil {
		return SessionPage{}, err
	}
	f = f.Normalize()

	m.mu.RLock()
	matched := make([]SessionRecord, 0, len(m.sessions))
	for _, s := range m.sessions {
		if matchSession(s.rec, f) {
			matched = append(matched, s.rec)
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
		// Keyset paging: resume after the last id seen, so rows inserted
		// since the previous page cannot shift the window.
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
// A query for one session walks only that session's events.
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

	first, next := m.oldest, func(e *memEvent) *memEvent { return e.next }
	if f.SessionID != "" {
		first, next = nil, func(e *memEvent) *memEvent { return e.sessionNext }
		if s := m.sessions[f.SessionID]; s != nil {
			first = s.first
		}
	}

	matched := make([]EventRecord, 0, f.Limit)
	var total int64
	for e := first; e != nil; e = next(e) {
		if !matchEvent(e.EventRecord, f) {
			continue
		}
		total++
		if e.Seq <= after {
			continue
		}
		if len(matched) < f.Limit {
			matched = append(matched, e.EventRecord)
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
	byOperation := map[string]int64{}
	byRule := map[string]int64{}

	for _, s := range m.sessions {
		rec := &s.rec
		if !matchSession(*rec, f) {
			continue
		}
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

		// The retained events only. The budget may have dropped some that
		// the totals above count; EventsDropped says so.
		out.EventsDropped += rec.EventsDropped
		for e := s.first; e != nil; e = e.sessionNext {
			if e.Operation != "" {
				byOperation[string(e.Operation)]++
			}
			if e.Rule != "" {
				byRule[e.Rule]++
			}
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
// render a thousand bars, so a query returning them all pays scan cost for
// rows the UI drops.
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

// decodeCursor rejects a malformed token rather than restarting from page
// one, which would make a paging bug look like duplicated data.
func decodeCursor(s string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, errors.New("sidecar/store: malformed cursor")
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return cursor{}, errors.New("sidecar/store: malformed cursor")
	}
	return c, nil
}

// Compile-time proof the backend satisfies the contract.
var _ Store = (*MemoryStore)(nil)
