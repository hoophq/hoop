package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/audit"
	"github.com/hoophq/hoopinspect/session"
	"github.com/hoophq/hoopinspect/store"
	sqlitestore "github.com/hoophq/hoopinspect/store/sqlite"
)

// base is a fixed wall clock so ordering assertions do not depend on how fast
// the test machine writes.
var base = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *sqlitestore.Store {
	t.Helper()
	s, err := sqlitestore.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func write(t *testing.T, s *sqlitestore.Store, ev audit.Event) {
	t.Helper()
	if err := s.Write(context.Background(), ev); err != nil {
		t.Fatalf("Write(%s/%s): %v", ev.SessionID, ev.Kind, err)
	}
}

// ------------------------------------------------------------- write path

func TestSessionLifecycleCounters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	write(t, s, audit.Event{
		Kind: audit.KindSessionStart, SessionID: "s1", Timestamp: base,
		Principal: "alice", Protocol: hoopinspect.Postgres, Connection: "appdb",
		Metadata: map[string]string{"upstream": "db:5432"},
	})
	for i := range 3 {
		write(t, s, audit.Event{
			Kind: audit.KindStatement, SessionID: "s1", Timestamp: base.Add(time.Duration(i+1) * time.Second),
			Principal: "alice", Operation: hoopinspect.OpSelect, Statement: "SELECT 1", Allowed: true,
		})
	}
	write(t, s, audit.Event{
		Kind: audit.KindViolation, SessionID: "s1", Timestamp: base.Add(5 * time.Second),
		Principal: "alice", Operation: hoopinspect.OpDrop, Statement: "DROP TABLE users",
		Rule: "no-destructive",
	})
	write(t, s, audit.Event{
		Kind: audit.KindMasked, SessionID: "s1", Timestamp: base.Add(6 * time.Second),
		Principal: "alice", MaskedEntities: []string{"email", "ssn"}, MaskedCount: 7,
	})
	write(t, s, audit.Event{
		Kind: audit.KindError, SessionID: "s1", Timestamp: base.Add(7 * time.Second),
		Principal: "alice", Error: "upstream reset",
	})
	write(t, s, audit.Event{
		Kind: audit.KindSessionEnd, SessionID: "s1", Timestamp: base.Add(10 * time.Second),
		Principal: "alice", Duration: 10 * time.Second,
		StatementCount: 4, DeniedCount: 1,
	})

	rec, err := s.Session(ctx, "s1")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	// A violation counts as a statement seen AND a denial: an auditor reads
	// statement_count as "what was attempted", not "what was allowed".
	if rec.StatementCount != 4 {
		t.Errorf("StatementCount = %d, want 4", rec.StatementCount)
	}
	if rec.DeniedCount != 1 {
		t.Errorf("DeniedCount = %d, want 1", rec.DeniedCount)
	}
	if rec.MaskedCount != 7 {
		t.Errorf("MaskedCount = %d, want 7 (the value count, not the event count)", rec.MaskedCount)
	}
	if rec.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", rec.ErrorCount)
	}
	if rec.Verdict != store.VerdictDenied {
		t.Errorf("Verdict = %q, want %q: a denial outranks the error", rec.Verdict, store.VerdictDenied)
	}
	if rec.IsOpen() {
		t.Error("IsOpen after session_end")
	}
	if !rec.StartedAt.Equal(base) {
		t.Errorf("StartedAt = %v, want %v", rec.StartedAt, base)
	}
	if !rec.EndedAt.Equal(base.Add(10 * time.Second)) {
		t.Errorf("EndedAt = %v", rec.EndedAt)
	}
	if rec.DurationMS != 10000 {
		t.Errorf("DurationMS = %d, want 10000", rec.DurationMS)
	}
	if rec.Principal != "alice" || rec.Connection != "appdb" || rec.Protocol != hoopinspect.Postgres {
		t.Errorf("session facts not carried: %+v", rec)
	}
	if rec.Upstream != "db:5432" {
		t.Errorf("Upstream = %q, want db:5432", rec.Upstream)
	}
}

func TestVerdictPrecedence(t *testing.T) {
	cases := []struct {
		name string
		ev   []audit.Kind
		want string
	}{
		{"clean", []audit.Kind{audit.KindStatement}, store.VerdictClean},
		{"error only", []audit.Kind{audit.KindStatement, audit.KindError}, store.VerdictError},
		{"denied only", []audit.Kind{audit.KindViolation}, store.VerdictDenied},
		{"denied outranks error", []audit.Kind{audit.KindError, audit.KindViolation}, store.VerdictDenied},
		{"error after denial stays denied", []audit.Kind{audit.KindViolation, audit.KindError}, store.VerdictDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			for i, k := range tc.ev {
				write(t, s, audit.Event{Kind: k, SessionID: "v", Timestamp: base.Add(time.Duration(i) * time.Second)})
			}
			rec, err := s.Session(context.Background(), "v")
			if err != nil {
				t.Fatal(err)
			}
			if rec.Verdict != tc.want {
				t.Errorf("Verdict = %q, want %q", rec.Verdict, tc.want)
			}
		})
	}
}

func TestEventWithoutSessionStartCreatesSession(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// A sink attached mid-session never sees session_start. Dropping the
	// event, or recording it under no session, makes it invisible to the list
	// view, which is the only screen anyone opens.
	write(t, s, audit.Event{
		Kind: audit.KindViolation, SessionID: "orphan", Timestamp: base,
		Principal: "bob", Protocol: hoopinspect.MySQL, Connection: "reports",
		Statement: "DELETE FROM audit", Rule: "no-delete",
	})

	rec, err := s.Session(ctx, "orphan")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if rec.Principal != "bob" || rec.Connection != "reports" || rec.Protocol != hoopinspect.MySQL {
		t.Errorf("facts not seeded from the first event: %+v", rec)
	}
	if rec.StatementCount != 1 || rec.DeniedCount != 1 {
		t.Errorf("counters = %d/%d, want 1/1", rec.StatementCount, rec.DeniedCount)
	}
	if rec.Verdict != store.VerdictDenied {
		t.Errorf("Verdict = %q", rec.Verdict)
	}
	if !rec.IsOpen() {
		t.Error("session with no session_end reported closed")
	}

	// A late session_start must not move the row's start time forward past
	// the event that created it, or the session sorts wrong in the list.
	write(t, s, audit.Event{
		Kind: audit.KindSessionStart, SessionID: "orphan",
		Timestamp: base.Add(time.Hour), Principal: "bob",
	})
	rec, err = s.Session(ctx, "orphan")
	if err != nil {
		t.Fatal(err)
	}
	if !rec.StartedAt.Equal(base) {
		t.Errorf("StartedAt = %v, want the earliest timestamp seen (%v)", rec.StartedAt, base)
	}
}

func TestSessionEndTotalsOverrideAccumulated(t *testing.T) {
	s := newStore(t)

	// The gate's own tally saw every statement; an AsyncSink between them may
	// not have. When session_end carries totals they win.
	write(t, s, audit.Event{Kind: audit.KindStatement, SessionID: "s", Timestamp: base})
	write(t, s, audit.Event{
		Kind: audit.KindSessionEnd, SessionID: "s", Timestamp: base.Add(time.Second),
		StatementCount: 50, DeniedCount: 4, Duration: time.Second,
	})

	rec, err := s.Session(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if rec.StatementCount != 50 || rec.DeniedCount != 4 {
		t.Errorf("counters = %d/%d, want the gate totals 50/4", rec.StatementCount, rec.DeniedCount)
	}
	if rec.Verdict != store.VerdictDenied {
		t.Errorf("Verdict = %q: the final totals must drive it", rec.Verdict)
	}
}

func TestRiskLevelKeepsHighest(t *testing.T) {
	// 'high' sorts BEFORE 'low' lexically, so a naive MAX() reports "low" for
	// a session that had a high-risk statement. This is the bug that test
	// exists for.
	cases := []struct {
		name   string
		levels []string
		want   string
	}{
		{"low then high", []string{"low", "high"}, "high"},
		{"high then low", []string{"high", "low"}, "high"},
		{"medium then low", []string{"medium", "low"}, "medium"},
		{"low then medium", []string{"low", "medium"}, "medium"},
		{"none", []string{"", ""}, ""},
		{"unanalyzed after high", []string{"high", ""}, "high"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			for i, lvl := range tc.levels {
				ev := audit.Event{
					Kind: audit.KindStatement, SessionID: "r",
					Timestamp: base.Add(time.Duration(i) * time.Second),
				}
				if lvl != "" {
					ev.Metadata = map[string]string{"risk_level": lvl}
				}
				write(t, s, ev)
			}
			rec, err := s.Session(context.Background(), "r")
			if err != nil {
				t.Fatal(err)
			}
			if rec.RiskLevel != tc.want {
				t.Errorf("RiskLevel = %q, want %q", rec.RiskLevel, tc.want)
			}
		})
	}
}

func TestWriteRejectsEventWithoutSessionID(t *testing.T) {
	s := newStore(t)
	// An event with no session cannot be correlated to anything. Storing it
	// under "" would pool every such event into one phantom session.
	if err := s.Write(context.Background(), audit.Event{Kind: audit.KindStatement}); err == nil {
		t.Fatal("Write = nil, want an error for an event with no session id")
	}
}

func TestEventRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	want := audit.Event{
		Kind: audit.KindStatement, SessionID: "rt", Timestamp: base,
		Principal: "carol", Protocol: hoopinspect.HTTP, Connection: "api",
		Operation: hoopinspect.OpPost, Statement: `{"q":"x"}`,
		Tables:  []string{"users", "orders"},
		Allowed: true, Rule: "allow-api", Message: "ok",
		Direction:      hoopinspect.FromClient,
		MaskedEntities: []string{"email"}, MaskedCount: 2,
		Error: "none", Duration: 3 * time.Second,
		StatementCount: 9, DeniedCount: 1,
		HTTP: &hoopinspect.HTTPDetail{
			Method: "POST", Path: "/v1/users/42", Resource: "/v1/users/*",
			StatusCode: 201, Host: "api.internal",
			Query:   map[string][]string{"debug": {"1"}},
			Headers: map[string]string{"content-type": "application/json"},
		},
		Metadata: map[string]string{"trace": "abc"},
	}
	write(t, s, want)

	page, err := s.Events(ctx, store.EventFilter{SessionID: "rt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(page.Events))
	}
	got := page.Events[0]
	if got.Seq <= 0 {
		t.Errorf("Seq = %d, want a positive sequence", got.Seq)
	}
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, want.Timestamp)
	}
	got.Timestamp = want.Timestamp
	if !reflect.DeepEqual(got.Event, want) {
		t.Errorf("round trip lost data:\n got %+v\nwant %+v", got.Event, want)
	}
	if got.HTTP == nil || got.HTTP.Resource != "/v1/users/*" || got.HTTP.Query["debug"][0] != "1" {
		t.Errorf("HTTP detail lost: %+v", got.HTTP)
	}
}

// -------------------------------------------------------------- read path

func TestSessionNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.Session(context.Background(), "nope")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want store.ErrNotFound", err)
	}
}

// seedSessions creates n sessions one second apart, oldest first.
func seedSessions(t *testing.T, s *sqlitestore.Store, n int) []session.ID {
	t.Helper()
	ids := make([]session.ID, n)
	for i := range n {
		id := session.ID(fmt.Sprintf("s%03d", i))
		ids[i] = id
		write(t, s, audit.Event{
			Kind: audit.KindSessionStart, SessionID: id,
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Principal: "alice",
		})
	}
	return ids
}

func TestSessionsNewestFirst(t *testing.T) {
	s := newStore(t)
	ids := seedSessions(t, s, 5)

	page, err := s.Sessions(context.Background(), store.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 {
		t.Errorf("Total = %d, want 5", page.Total)
	}
	for i, rec := range page.Sessions {
		want := ids[len(ids)-1-i]
		if rec.ID != want {
			t.Fatalf("session[%d] = %q, want %q (newest first)", i, rec.ID, want)
		}
	}
}

// TestSessionKeysetPagingUnderConcurrentInserts is the reason paging is
// keyset and not OFFSET. New sessions land at the FRONT of a newest-first
// listing, so an OFFSET cursor points one row further into a list that grew
// underneath it and serves a row the caller already read. On a live audit
// trail sessions open constantly, so this is the steady state, not an edge.
func TestSessionKeysetPagingUnderConcurrentInserts(t *testing.T) {
	const seeded, pageSize = 20, 5

	t.Run("keyset sees every row exactly once", func(t *testing.T) {
		s := newStore(t)
		want := seedSessions(t, s, seeded)

		seen := map[session.ID]int{}
		cursor := ""
		extra := 0
		for {
			page, err := s.Sessions(context.Background(), store.SessionFilter{
				Limit: pageSize, Cursor: cursor,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, rec := range page.Sessions {
				seen[rec.ID]++
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor

			// A new session opens mid-page, newer than everything seeded.
			extra++
			write(t, s, audit.Event{
				Kind:      audit.KindSessionStart,
				SessionID: session.ID(fmt.Sprintf("new%02d", extra)),
				Timestamp: base.Add(time.Hour + time.Duration(extra)*time.Second),
			})
		}

		if extra == 0 {
			t.Fatal("test did not actually insert during paging")
		}
		for _, id := range want {
			switch seen[id] {
			case 1:
			case 0:
				t.Errorf("session %q was skipped", id)
			default:
				t.Errorf("session %q was returned %d times", id, seen[id])
			}
		}
	})

	// The same loop against OFFSET paging, to prove the bug is real rather
	// than theoretical. If this ever stops failing, the concurrent insert
	// above stopped happening and the keyset test above went vacuous.
	t.Run("offset paging is why", func(t *testing.T) {
		s := newStore(t)
		want := seedSessions(t, s, seeded)

		seen := map[string]int{}
		extra := 0
		for offset := 0; offset < seeded; offset += pageSize {
			rows, err := s.DB().QueryContext(context.Background(),
				`SELECT id FROM sessions ORDER BY started_at DESC, id DESC LIMIT ? OFFSET ?`,
				pageSize, offset)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				seen[id]++
			}
			rows.Close()

			extra++
			write(t, s, audit.Event{
				Kind:      audit.KindSessionStart,
				SessionID: session.ID(fmt.Sprintf("new%02d", extra)),
				Timestamp: base.Add(time.Hour + time.Duration(extra)*time.Second),
			})
		}

		correct := true
		for _, id := range want {
			if seen[string(id)] != 1 {
				correct = false
			}
		}
		if correct {
			t.Fatal("OFFSET paging returned every row exactly once; " +
				"the concurrent-insert scenario this guards is no longer being exercised")
		}
	})
}

// TestSessionPagingBreaksTimestampTies pins the id half of the (started_at,
// id) cursor. Sessions opened in the same microsecond are ordinary under
// load, and a cursor keyed on started_at alone would either skip every tied
// row after the page boundary or replay all of them forever.
func TestSessionPagingBreaksTimestampTies(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	const n, pageSize = 9, 4
	want := make([]session.ID, n)
	for i := range n {
		want[i] = session.ID(fmt.Sprintf("tie%02d", i))
		// One shared timestamp: id is the only thing separating these rows.
		write(t, s, audit.Event{Kind: audit.KindSessionStart, SessionID: want[i], Timestamp: base})
	}

	seen := map[session.ID]int{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > n {
			t.Fatal("paging did not terminate; the cursor is replaying tied rows")
		}
		page, err := s.Sessions(ctx, store.SessionFilter{Limit: pageSize, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range page.Sessions {
			seen[rec.ID]++
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	for _, id := range want {
		if seen[id] != 1 {
			t.Errorf("session %q returned %d times across pages, want exactly 1", id, seen[id])
		}
	}
}

func TestEventKeysetPagingUnderConcurrentInserts(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	const seeded, pageSize = 20, 5
	for i := range seeded {
		write(t, s, audit.Event{
			Kind: audit.KindStatement, SessionID: "e",
			// Every event shares one timestamp on purpose: at millisecond (or
			// even microsecond) resolution real events collide, and a cursor
			// keyed on the timestamp would then skip or repeat rows. seq is
			// the only total order.
			Timestamp: base,
			Statement: fmt.Sprintf("SELECT %d", i),
		})
	}

	seenSeq := map[int64]int{}
	var order []int64
	cursor := ""
	extra := 0
	for {
		page, err := s.Events(ctx, store.EventFilter{SessionID: "e", Limit: pageSize, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range page.Events {
			seenSeq[rec.Seq]++
			order = append(order, rec.Seq)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor

		extra++
		write(t, s, audit.Event{
			Kind: audit.KindStatement, SessionID: "e", Timestamp: base,
			Statement: fmt.Sprintf("concurrent %d", extra),
		})
	}

	if extra == 0 {
		t.Fatal("test did not insert during paging")
	}
	if len(seenSeq) < seeded {
		t.Errorf("saw %d distinct events, want at least the %d seeded", len(seenSeq), seeded)
	}
	for seq, n := range seenSeq {
		if n != 1 {
			t.Errorf("seq %d returned %d times", seq, n)
		}
	}
	for i := 1; i < len(order); i++ {
		if order[i] <= order[i-1] {
			t.Fatalf("events not strictly ascending across pages: %d then %d", order[i-1], order[i])
		}
	}
}

func TestPageCursorEmptyOnLastPage(t *testing.T) {
	s := newStore(t)
	seedSessions(t, s, 6)

	// A cursor handed out on an exactly-full final page leads to an empty
	// page, which a UI renders as a phantom "next".
	page, err := s.Sessions(context.Background(), store.SessionFilter{Limit: 6})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 6 {
		t.Fatalf("got %d sessions, want 6", len(page.Sessions))
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor = %q on an exhausted result set, want empty", page.NextCursor)
	}
}

func TestMalformedCursorErrors(t *testing.T) {
	s := newStore(t)
	seedSessions(t, s, 3)
	ctx := context.Background()

	// Silently restarting at page one turns a client bug into an infinite
	// loop that re-reads the first page forever.
	bad := []string{
		"not base64!!",
		"////",
		"eyJib2d1cyI6MX0",             // valid base64 JSON, unknown field
		"eyJzIjoxfXt9",                // trailing data after the object
		"c29tZSByYW5kb20gYnl0ZXM",     // base64 of non-JSON
		"eyJzIjogInN0cmluZyIsICJpIjo", // truncated
	}
	for _, c := range bad {
		t.Run("sessions/"+c, func(t *testing.T) {
			if _, err := s.Sessions(ctx, store.SessionFilter{Cursor: c}); err == nil {
				t.Fatalf("Sessions with cursor %q = nil error, want a rejection", c)
			}
		})
		t.Run("events/"+c, func(t *testing.T) {
			if _, err := s.Events(ctx, store.EventFilter{Cursor: c}); err == nil {
				t.Fatalf("Events with cursor %q = nil error, want a rejection", c)
			}
		})
	}
}

func TestLimitClampedToMax(t *testing.T) {
	s := newStore(t)
	seedSessions(t, s, 3)

	// Normalize is the single place limits are enforced; a backend that
	// bypasses it turns a hand-crafted API request into an unbounded scan.
	page, err := s.Sessions(context.Background(), store.SessionFilter{Limit: store.MaxLimit + 5000})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(page.Sessions))
	}
}

// --------------------------------------------------------------- filters

// seedFilterCorpus builds a small corpus where every filter field has both a
// matching and a non-matching row, so a filter that is silently ignored shows
// up as too many results.
func seedFilterCorpus(t *testing.T, s *sqlitestore.Store) {
	t.Helper()

	write(t, s, audit.Event{
		Kind: audit.KindSessionStart, SessionID: "a", Timestamp: base,
		Principal: "alice", Protocol: hoopinspect.Postgres, Connection: "appdb",
	})
	write(t, s, audit.Event{
		Kind: audit.KindStatement, SessionID: "a", Timestamp: base.Add(time.Second),
		Principal: "alice", Protocol: hoopinspect.Postgres, Connection: "appdb",
		Operation: hoopinspect.OpSelect, Statement: "SELECT secret FROM vault", Allowed: true,
	})
	write(t, s, audit.Event{
		Kind: audit.KindSessionEnd, SessionID: "a", Timestamp: base.Add(2 * time.Second),
		Principal: "alice", Protocol: hoopinspect.Postgres, Connection: "appdb",
		Duration: 2 * time.Second, StatementCount: 1,
	})

	// bob's session is denied, on a different connection and protocol, an
	// hour later, and left open.
	write(t, s, audit.Event{
		Kind: audit.KindSessionStart, SessionID: "b", Timestamp: base.Add(time.Hour),
		Principal: "bob", Protocol: hoopinspect.MySQL, Connection: "reports",
	})
	write(t, s, audit.Event{
		Kind: audit.KindViolation, SessionID: "b", Timestamp: base.Add(time.Hour + time.Second),
		Principal: "bob", Protocol: hoopinspect.MySQL, Connection: "reports",
		Operation: hoopinspect.OpDrop, Statement: "DROP TABLE ledger", Rule: "no-destructive",
	})
}

func TestSessionFilterFieldsNarrow(t *testing.T) {
	s := newStore(t)
	seedFilterCorpus(t, s)
	ctx := context.Background()

	cases := []struct {
		name string
		f    store.SessionFilter
		want []session.ID
	}{
		{"zero filter lists all", store.SessionFilter{}, []session.ID{"b", "a"}},
		{"principal", store.SessionFilter{Principal: "alice"}, []session.ID{"a"}},
		{"principal is exact, not prefix", store.SessionFilter{Principal: "ali"}, nil},
		{"connection", store.SessionFilter{Connection: "reports"}, []session.ID{"b"}},
		{"protocol", store.SessionFilter{Protocol: hoopinspect.Postgres}, []session.ID{"a"}},
		{"denied only", store.SessionFilter{DeniedOnly: true}, []session.ID{"b"}},
		{"open only", store.SessionFilter{OpenOnly: true}, []session.ID{"b"}},
		{"since", store.SessionFilter{Since: base.Add(30 * time.Minute)}, []session.ID{"b"}},
		{"since is inclusive", store.SessionFilter{Since: base.Add(time.Hour)}, []session.ID{"b"}},
		{"until is exclusive", store.SessionFilter{Until: base.Add(time.Hour)}, []session.ID{"a"}},
		{"window", store.SessionFilter{Since: base, Until: base.Add(time.Minute)}, []session.ID{"a"}},
		{"search principal", store.SessionFilter{Search: "ALI"}, []session.ID{"a"}},
		{"search connection", store.SessionFilter{Search: "report"}, []session.ID{"b"}},
		{"search statement text", store.SessionFilter{Search: "ledger"}, []session.ID{"b"}},
		{"search misses", store.SessionFilter{Search: "nothing here"}, nil},
		{"combined", store.SessionFilter{Principal: "bob", DeniedOnly: true}, []session.ID{"b"}},
		{"combined contradiction", store.SessionFilter{Principal: "alice", DeniedOnly: true}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.Sessions(ctx, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]session.ID, len(page.Sessions))
			for i, rec := range page.Sessions {
				got[i] = rec.ID
			}
			if !equalIDs(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
			if page.Total != int64(len(tc.want)) {
				t.Errorf("Total = %d, want %d", page.Total, len(tc.want))
			}
		})
	}
}

func TestEventFilterFieldsNarrow(t *testing.T) {
	s := newStore(t)
	seedFilterCorpus(t, s)
	ctx := context.Background()

	cases := []struct {
		name string
		f    store.EventFilter
		want int
	}{
		{"zero filter lists all", store.EventFilter{}, 5},
		{"session id", store.EventFilter{SessionID: "a"}, 3},
		{"one kind", store.EventFilter{Kinds: []audit.Kind{audit.KindStatement}}, 1},
		{"several kinds", store.EventFilter{Kinds: []audit.Kind{audit.KindSessionStart, audit.KindSessionEnd}}, 3},
		{"kind and session", store.EventFilter{SessionID: "a", Kinds: []audit.Kind{audit.KindSessionStart}}, 1},
		{"principal", store.EventFilter{Principal: "bob"}, 2},
		{"connection", store.EventFilter{Connection: "appdb"}, 3},
		{"protocol", store.EventFilter{Protocol: hoopinspect.MySQL}, 2},
		{"denied only", store.EventFilter{DeniedOnly: true}, 1},
		{"since", store.EventFilter{Since: base.Add(time.Hour)}, 2},
		{"until exclusive", store.EventFilter{Until: base.Add(time.Second)}, 1},
		{"window", store.EventFilter{Since: base, Until: base.Add(2 * time.Second)}, 2},
		{"search", store.EventFilter{Search: "drop table"}, 1},
		{"search misses", store.EventFilter{Search: "zzz"}, 0},
		{"combined", store.EventFilter{Principal: "bob", DeniedOnly: true}, 1},
		{"combined contradiction", store.EventFilter{Principal: "alice", DeniedOnly: true}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.Events(ctx, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Events) != tc.want {
				kinds := make([]string, len(page.Events))
				for i, e := range page.Events {
					kinds[i] = string(e.Kind)
				}
				t.Errorf("got %d events %v, want %d", len(page.Events), kinds, tc.want)
			}
			if page.Total != int64(tc.want) {
				t.Errorf("Total = %d, want %d", page.Total, tc.want)
			}
		})
	}
}

func TestSearchEscapesWildcards(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// Each pair is chosen so an UNESCAPED needle matches both rows and an
	// escaped one matches only the literal. A needle whose wildcard is
	// redundant (the leading "100" of "100%") proves nothing, because the
	// prefix already excludes the other row.
	rows := []string{
		`discount 50% off`, // literal percent
		`discount 50 then off`,
		`col a_b`, // literal underscore
		`col axb`,
		`path C:\tmp`, // literal backslash
		`path C:tmp`,
	}
	for i, stmt := range rows {
		write(t, s, audit.Event{
			Kind: audit.KindStatement, SessionID: session.ID(fmt.Sprintf("w%d", i)),
			Timestamp: base, Statement: stmt,
		})
	}

	cases := []struct{ needle, want string }{
		{`50% off`, `discount 50% off`},
		{`a_b`, `col a_b`},
		{`C:\tmp`, `path C:\tmp`},
	}
	for _, tc := range cases {
		t.Run(tc.needle, func(t *testing.T) {
			page, err := s.Events(ctx, store.EventFilter{Search: tc.needle})
			if err != nil {
				t.Fatalf("Events: %v", err)
			}
			if len(page.Events) != 1 {
				got := make([]string, len(page.Events))
				for i, e := range page.Events {
					got[i] = e.Statement
				}
				t.Fatalf("search %q matched %v, want only the literal row", tc.needle, got)
			}
			if page.Events[0].Statement != tc.want {
				t.Errorf("matched %q, want %q", page.Events[0].Statement, tc.want)
			}
		})
	}
}

// ----------------------------------------------------------------- stats

func TestStatsTotalsHonorFilter(t *testing.T) {
	s := newStore(t)
	seedFilterCorpus(t, s)

	all, err := s.Stats(context.Background(), store.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if all.Sessions != 2 || all.Statements != 2 || all.Denied != 1 {
		t.Errorf("stats = %+v, want 2 sessions / 2 statements / 1 denied", all)
	}

	// A dashboard and the list underneath it must agree on the population.
	scoped, err := s.Stats(context.Background(), store.SessionFilter{Principal: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Sessions != 1 || scoped.Denied != 0 {
		t.Errorf("filtered stats = %+v, want alice's 1 clean session", scoped)
	}
	if len(scoped.ByRule) != 0 {
		t.Errorf("ByRule = %v, want empty: bob's rule is out of scope", scoped.ByRule)
	}
}

func TestStatsBreakdownsSortedAndTruncated(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// principal_i gets i+1 sessions, so the expected descending order is
	// known and the tail is what truncation must drop.
	const principals = store.TopN + 7
	for i := range principals {
		for j := range i + 1 {
			id := session.ID(fmt.Sprintf("p%02d-%02d", i, j))
			write(t, s, audit.Event{
				Kind: audit.KindSessionStart, SessionID: id,
				Timestamp: base.Add(time.Duration(i*100+j) * time.Second),
				Principal: fmt.Sprintf("principal_%02d", i),
			})
		}
	}

	st, err := s.Stats(ctx, store.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.ByPrincipal) != store.TopN {
		t.Fatalf("ByPrincipal has %d entries, want TopN=%d", len(st.ByPrincipal), store.TopN)
	}
	for i := 1; i < len(st.ByPrincipal); i++ {
		if st.ByPrincipal[i].Count > st.ByPrincipal[i-1].Count {
			t.Fatalf("ByPrincipal not descending at %d: %+v", i, st.ByPrincipal)
		}
	}
	// Truncation must keep the LARGEST bars; dropping those instead would
	// still produce a sorted list of the right length.
	if got, want := st.ByPrincipal[0].Count, int64(principals); got != want {
		t.Errorf("top bar count = %d, want the largest principal %d", got, want)
	}
	if got := st.ByPrincipal[store.TopN-1].Count; got != int64(principals-store.TopN+1) {
		t.Errorf("last kept bar count = %d, want %d", got, principals-store.TopN+1)
	}
}

func TestStatsBreakdownUnits(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// One session, three selects. ByPrincipal counts SESSIONS (1);
	// ByOperation counts EVENTS (3). Conflating them makes a chart lie.
	write(t, s, audit.Event{Kind: audit.KindSessionStart, SessionID: "u",
		Timestamp: base, Principal: "dana", Connection: "appdb"})
	for i := range 3 {
		write(t, s, audit.Event{Kind: audit.KindStatement, SessionID: "u",
			Timestamp: base.Add(time.Duration(i+1) * time.Second),
			Principal: "dana", Connection: "appdb", Operation: hoopinspect.OpSelect})
	}
	write(t, s, audit.Event{Kind: audit.KindViolation, SessionID: "u",
		Timestamp: base.Add(10 * time.Second), Principal: "dana", Connection: "appdb",
		Operation: hoopinspect.OpDrop, Rule: "no-destructive"})

	st, err := s.Stats(ctx, store.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.ByPrincipal) != 1 || st.ByPrincipal[0].Count != 1 {
		t.Errorf("ByPrincipal = %+v, want one session for dana", st.ByPrincipal)
	}
	if len(st.ByConnection) != 1 || st.ByConnection[0].Count != 1 {
		t.Errorf("ByConnection = %+v, want one session on appdb", st.ByConnection)
	}
	if len(st.ByOperation) != 2 {
		t.Fatalf("ByOperation = %+v, want select and drop", st.ByOperation)
	}
	if st.ByOperation[0].Label != string(hoopinspect.OpSelect) || st.ByOperation[0].Count != 3 {
		t.Errorf("ByOperation[0] = %+v, want 3 selects first", st.ByOperation[0])
	}
	if len(st.ByRule) != 1 || st.ByRule[0].Count != 1 {
		t.Errorf("ByRule = %+v, want one no-destructive hit", st.ByRule)
	}
}

func TestStatsByRisk(t *testing.T) {
	s := newStore(t)

	for i, lvl := range []string{"high", "high", "low", "medium"} {
		id := session.ID(fmt.Sprintf("risk%d", i))
		write(t, s, audit.Event{
			Kind: audit.KindStatement, SessionID: id,
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Metadata:  map[string]string{"risk_level": lvl},
		})
	}
	// A session with no analysis must not become a phantom "" bar.
	write(t, s, audit.Event{Kind: audit.KindStatement, SessionID: "unanalyzed",
		Timestamp: base.Add(time.Minute)})

	st, err := s.Stats(context.Background(), store.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []store.LabelCount{{Label: "high", Count: 2}, {Label: "low", Count: 1}, {Label: "medium", Count: 1}}
	if fmt.Sprint(st.ByRisk) != fmt.Sprint(want) {
		t.Errorf("ByRisk = %v, want %v (descending, ties broken by label)", st.ByRisk, want)
	}
}

// ------------------------------------------------------- concurrency & ctx

func TestConcurrentWritesLoseNoEvents(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	const workers, perWorker = 24, 25

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWorker {
				// Half the writes share one session so its denormalized
				// counters are contended: the shared row's final count must
				// equal the events actually stored, not a value a lost
				// update left behind.
				id := session.ID("shared")
				if w%2 == 0 {
					id = session.ID(fmt.Sprintf("w%02d", w))
				}
				err := s.Write(ctx, audit.Event{
					Kind: audit.KindStatement, SessionID: id, Timestamp: base,
					Principal: "load", Statement: strings.Repeat("x", 200),
				})
				if err != nil {
					errs <- fmt.Errorf("worker %d write %d: %w", w, i, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	page, err := s.Events(ctx, store.EventFilter{Limit: store.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(workers * perWorker); page.Total != want {
		t.Errorf("stored %d events, want %d", page.Total, want)
	}

	rec, err := s.Session(ctx, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if want := workers / 2 * perWorker; rec.StatementCount != want {
		t.Errorf("shared session StatementCount = %d, want %d: the counter "+
			"desynchronized from the event rows", rec.StatementCount, want)
	}
}

func TestContextCancellationHonored(t *testing.T) {
	s := newStore(t)
	seedFilterCorpus(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("write", func(t *testing.T) {
		err := s.Write(ctx, audit.Event{Kind: audit.KindStatement, SessionID: "x", Timestamp: base})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Write = %v, want context.Canceled", err)
		}
	})
	t.Run("sessions", func(t *testing.T) {
		if _, err := s.Sessions(ctx, store.SessionFilter{}); !errors.Is(err, context.Canceled) {
			t.Errorf("Sessions = %v, want context.Canceled", err)
		}
	})
	t.Run("session", func(t *testing.T) {
		if _, err := s.Session(ctx, "a"); !errors.Is(err, context.Canceled) {
			t.Errorf("Session = %v, want context.Canceled", err)
		}
	})
	t.Run("events", func(t *testing.T) {
		if _, err := s.Events(ctx, store.EventFilter{}); !errors.Is(err, context.Canceled) {
			t.Errorf("Events = %v, want context.Canceled", err)
		}
	})
	t.Run("stats", func(t *testing.T) {
		if _, err := s.Stats(ctx, store.SessionFilter{}); !errors.Is(err, context.Canceled) {
			t.Errorf("Stats = %v, want context.Canceled", err)
		}
	})
}

// ------------------------------------------------------------- open/close

func TestDoubleCloseIsSafe(t *testing.T) {
	s, err := sqlitestore.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	// audit.Sink requires Close to be safe twice: a defer chain that closes
	// the gate and the sink independently double-closes routinely.
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestOpenPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")

	s, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	write(t, s, audit.Event{
		Kind: audit.KindViolation, SessionID: "persist", Timestamp: base,
		Principal: "erin", Statement: "TRUNCATE t", Rule: "no-truncate",
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening must find the schema already there (CREATE TABLE IF NOT
	// EXISTS) and the rows intact.
	s2, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	rec, err := s2.Session(context.Background(), "persist")
	if err != nil {
		t.Fatalf("Session after reopen: %v", err)
	}
	if rec.DeniedCount != 1 || rec.Verdict != store.VerdictDenied {
		t.Errorf("record did not survive reopen: %+v", rec)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := sqlitestore.Open(""); err == nil {
		t.Fatal("Open(\"\") = nil error; an empty path would silently create a temp database")
	}
}

func TestSchemaVersionRecordedAndGuarded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.db")
	ctx := context.Background()

	s, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := s.DB().QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		t.Fatalf("schema_version table missing, so a future migration has nowhere to stand: %v", err)
	}
	if v != 1 {
		t.Errorf("schema_version = %d, want 1", v)
	}

	// Simulate a newer binary having written this file. Serving audit data
	// out of columns a newer writer may have repurposed is worse than
	// refusing to open.
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO schema_version(version) VALUES (99)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = sqlitestore.Open(path)
	if err == nil {
		t.Fatal("Open on a future schema version succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("error = %v, want it to name the version mismatch", err)
	}
}

func TestMemoryStoresAreIsolated(t *testing.T) {
	// Two in-memory stores in one test binary must not share a database, or
	// a test's assertions are polluted by another's writes.
	a := newStore(t)
	b := newStore(t)

	write(t, a, audit.Event{Kind: audit.KindStatement, SessionID: "only-in-a", Timestamp: base})

	if _, err := b.Session(context.Background(), "only-in-a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second store saw the first store's rows: %v", err)
	}
}

func TestMemoryDatabaseSurvivesPoolChurn(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	write(t, s, audit.Event{Kind: audit.KindStatement, SessionID: "pinned", Timestamp: base})

	// A shared-cache memory database is destroyed when its LAST connection
	// closes. Forcing the pool to retire every idle connection reproduces
	// that: without a pinned connection the schema disappears and the next
	// query fails with "no such table".
	s.DB().SetMaxIdleConns(0)
	for range 20 {
		if _, err := s.DB().ExecContext(ctx, `SELECT 1`); err != nil {
			t.Fatalf("churn query: %v", err)
		}
	}
	s.DB().SetMaxIdleConns(2)

	if _, err := s.Session(ctx, "pinned"); err != nil {
		t.Fatalf("Session after pool churn: %v", err)
	}
}

func equalIDs(a, b []session.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
