package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/hoopinspect"
	"github.com/hoophq/hoop/hoopinspect/audit"
	"github.com/hoophq/hoop/hoopinspect/session"
	"github.com/hoophq/hoop/hoopinspect/store"
)

func ctx() context.Context { return context.Background() }

// seed writes a realistic session: start, some statements, a denial, end.
func seed(t *testing.T, s store.Store, id session.ID, principal, conn string, denied bool) {
	t.Helper()
	base := time.Now().UTC().Add(-time.Minute)

	write := func(ev audit.Event) {
		t.Helper()
		if err := s.Write(ctx(), ev); err != nil {
			t.Fatalf("Write(%s): %v", ev.Kind, err)
		}
	}

	write(audit.Event{
		Kind: audit.KindSessionStart, Timestamp: base, SessionID: id,
		Principal: principal, Protocol: hoopinspect.Postgres, Connection: conn,
	})
	write(audit.Event{
		Kind: audit.KindStatement, Timestamp: base.Add(time.Second), SessionID: id,
		Principal: principal, Protocol: hoopinspect.Postgres, Connection: conn,
		Operation: hoopinspect.OpSelect, Statement: "SELECT name FROM customers",
		Tables: []string{"customers"}, Allowed: true,
	})
	if denied {
		write(audit.Event{
			Kind: audit.KindViolation, Timestamp: base.Add(2 * time.Second), SessionID: id,
			Principal: principal, Protocol: hoopinspect.Postgres, Connection: conn,
			Operation: hoopinspect.OpDrop, Statement: "DROP TABLE customers",
			Allowed: false, Rule: "no-destructive", Message: "not permitted",
		})
	}
	write(audit.Event{
		Kind: audit.KindSessionEnd, Timestamp: base.Add(3 * time.Second), SessionID: id,
		Principal: principal, Protocol: hoopinspect.Postgres, Connection: conn,
		Duration: 3 * time.Second,
	})
}

func TestSessionCountersAreDenormalized(t *testing.T) {
	s := store.NewMemoryStore(10)
	seed(t, s, "sess-1", "alice", "appdb", true)

	rec, err := s.Session(ctx(), "sess-1")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if rec.StatementCount != 2 {
		t.Errorf("StatementCount = %d, want 2", rec.StatementCount)
	}
	if rec.DeniedCount != 1 {
		t.Errorf("DeniedCount = %d, want 1", rec.DeniedCount)
	}
	if rec.Verdict != store.VerdictDenied {
		t.Errorf("Verdict = %q, want denied", rec.Verdict)
	}
	if rec.Principal != "alice" {
		t.Errorf("Principal = %q", rec.Principal)
	}
	if rec.IsOpen() {
		t.Error("session reports open after session_end")
	}
	if rec.DurationMS != 3000 {
		t.Errorf("DurationMS = %d, want 3000", rec.DurationMS)
	}
}

// A sink attached mid-session sees no session_start. Dropping those events
// would lose the statements that matter most.
func TestEventWithoutSessionStartStillCreatesASession(t *testing.T) {
	s := store.NewMemoryStore(10)
	err := s.Write(ctx(), audit.Event{
		Kind: audit.KindStatement, Timestamp: time.Now().UTC(), SessionID: "orphan",
		Principal: "bob", Protocol: hoopinspect.HTTP, Connection: "db",
		Operation: hoopinspect.OpSelect, Statement: "SELECT 1", Allowed: true,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	rec, err := s.Session(ctx(), "orphan")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if rec.StatementCount != 1 || rec.Principal != "bob" {
		t.Errorf("orphan session = %+v", rec)
	}
}

func TestSessionNotFound(t *testing.T) {
	s := store.NewMemoryStore(10)
	_, err := s.Session(ctx(), "nope")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSessionFiltersNarrow(t *testing.T) {
	s := store.NewMemoryStore(50)
	seed(t, s, "a", "alice", "appdb", true)
	seed(t, s, "b", "bob", "appdb", false)
	seed(t, s, "c", "alice", "api", false)

	cases := []struct {
		name   string
		filter store.SessionFilter
		want   int
	}{
		{"all", store.SessionFilter{}, 3},
		{"principal", store.SessionFilter{Principal: "alice"}, 2},
		{"connection", store.SessionFilter{Connection: "appdb"}, 2},
		{"protocol", store.SessionFilter{Protocol: hoopinspect.Postgres}, 3},
		{"other protocol", store.SessionFilter{Protocol: hoopinspect.HTTP}, 0},
		{"denied only", store.SessionFilter{DeniedOnly: true}, 1},
		{"search", store.SessionFilter{Search: "ALICE"}, 2},
		{"since future", store.SessionFilter{Since: time.Now().Add(time.Hour)}, 0},
		{"until past", store.SessionFilter{Until: time.Now().Add(-time.Hour)}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := s.Sessions(ctx(), tc.filter)
			if err != nil {
				t.Fatalf("Sessions: %v", err)
			}
			if len(page.Sessions) != tc.want {
				t.Errorf("got %d sessions, want %d", len(page.Sessions), tc.want)
			}
		})
	}
}

// Keyset paging must return every row exactly once even while new rows
// arrive. OFFSET paging fails here: an insert shifts the window, so a row is
// skipped or repeated.
func TestKeysetPagingIsStableUnderConcurrentInserts(t *testing.T) {
	s := store.NewMemoryStore(500)
	for i := range 20 {
		seed(t, s, session.ID(string(rune('a'+i))), "alice", "appdb", false)
	}

	seen := map[session.ID]int{}
	cursor := ""
	for range 10 {
		page, err := s.Sessions(ctx(), store.SessionFilter{Limit: 5, Cursor: cursor})
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		for _, rec := range page.Sessions {
			seen[rec.ID]++
		}
		// Insert a NEW session between pages, the way a live audit trail
		// does.
		seed(t, s, session.ID("new-"+cursor+string(rune(len(seen)))), "carol", "appdb", false)

		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	for id, n := range seen {
		if n > 1 {
			t.Errorf("session %q returned %d times across pages", id, n)
		}
	}
	if len(seen) < 20 {
		t.Errorf("saw %d of the original 20 sessions", len(seen))
	}
}

func TestMalformedCursorIsRejected(t *testing.T) {
	s := store.NewMemoryStore(10)
	seed(t, s, "a", "alice", "appdb", false)

	if _, err := s.Sessions(ctx(), store.SessionFilter{Cursor: "!!!not-base64!!!"}); err == nil {
		t.Error("a malformed session cursor was accepted")
	}
	if _, err := s.Events(ctx(), store.EventFilter{Cursor: "!!!not-base64!!!"}); err == nil {
		t.Error("a malformed event cursor was accepted")
	}
}

func TestEventsAreOldestFirstWithinASession(t *testing.T) {
	s := store.NewMemoryStore(10)
	seed(t, s, "a", "alice", "appdb", true)

	page, err := s.Events(ctx(), store.EventFilter{SessionID: "a"})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(page.Events) != 4 {
		t.Fatalf("got %d events, want 4", len(page.Events))
	}
	if page.Events[0].Kind != audit.KindSessionStart {
		t.Errorf("first event = %q, want session_start", page.Events[0].Kind)
	}
	if page.Events[len(page.Events)-1].Kind != audit.KindSessionEnd {
		t.Errorf("last event = %q, want session_end", page.Events[len(page.Events)-1].Kind)
	}
	for i := 1; i < len(page.Events); i++ {
		if page.Events[i].Seq <= page.Events[i-1].Seq {
			t.Error("event sequence is not strictly increasing")
		}
	}
}

func TestEventFilters(t *testing.T) {
	s := store.NewMemoryStore(10)
	seed(t, s, "a", "alice", "appdb", true)

	denied, _ := s.Events(ctx(), store.EventFilter{DeniedOnly: true})
	if len(denied.Events) != 1 || denied.Events[0].Kind != audit.KindViolation {
		t.Errorf("denied filter returned %d events", len(denied.Events))
	}

	kinds, _ := s.Events(ctx(), store.EventFilter{
		Kinds: []audit.Kind{audit.KindStatement, audit.KindViolation},
	})
	if len(kinds.Events) != 2 {
		t.Errorf("kind filter returned %d events, want 2", len(kinds.Events))
	}

	search, _ := s.Events(ctx(), store.EventFilter{Search: "drop table"})
	if len(search.Events) != 1 {
		t.Errorf("search returned %d events, want 1", len(search.Events))
	}
}

func TestStatsAggregates(t *testing.T) {
	s := store.NewMemoryStore(50)
	seed(t, s, "a", "alice", "appdb", true)
	seed(t, s, "b", "alice", "appdb", false)
	seed(t, s, "c", "bob", "api", false)

	st, err := s.Stats(ctx(), store.SessionFilter{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Sessions != 3 {
		t.Errorf("Sessions = %d, want 3", st.Sessions)
	}
	if st.Denied != 1 {
		t.Errorf("Denied = %d, want 1", st.Denied)
	}
	if len(st.ByPrincipal) == 0 || st.ByPrincipal[0].Label != "alice" {
		t.Errorf("ByPrincipal = %+v, want alice first", st.ByPrincipal)
	}
	// A chart needs descending order.
	for i := 1; i < len(st.ByPrincipal); i++ {
		if st.ByPrincipal[i].Count > st.ByPrincipal[i-1].Count {
			t.Error("ByPrincipal is not sorted descending")
		}
	}
	if len(st.ByRule) == 0 || st.ByRule[0].Label != "no-destructive" {
		t.Errorf("ByRule = %+v", st.ByRule)
	}
}

func TestStatsBreakdownTruncatedToTopN(t *testing.T) {
	s := store.NewMemoryStore(2000)
	for i := range store.TopN + 15 {
		id := session.ID("s" + strings.Repeat("x", i%5) + string(rune('A'+i)))
		seed(t, s, id, "user"+string(rune('A'+i)), "conn", false)
	}
	st, _ := s.Stats(ctx(), store.SessionFilter{})
	if len(st.ByPrincipal) > store.TopN {
		t.Errorf("ByPrincipal has %d entries, want at most %d", len(st.ByPrincipal), store.TopN)
	}
}

// Highest risk wins. An average lets one dangerous statement hide behind
// fifty harmless ones.
func TestRiskLevelTakesTheMaximum(t *testing.T) {
	s := store.NewMemoryStore(10)
	for _, level := range []string{"low", "high", "medium"} {
		s.Write(ctx(), audit.Event{
			Kind: audit.KindStatement, Timestamp: time.Now().UTC(), SessionID: "r",
			Principal: "alice", Operation: hoopinspect.OpSelect, Allowed: true,
			Metadata: map[string]string{"risk_level": level},
		})
	}
	rec, _ := s.Session(ctx(), "r")
	if rec.RiskLevel != "high" {
		t.Errorf("RiskLevel = %q, want high", rec.RiskLevel)
	}
}

// Eviction must drop a whole session, never half its timeline, or the detail
// view renders a truncated session as a whole one.
func TestEvictionDropsWholeSessions(t *testing.T) {
	s := store.NewMemoryStore(2)
	seed(t, s, "old", "alice", "appdb", false)
	seed(t, s, "mid", "bob", "appdb", false)
	seed(t, s, "new", "carol", "appdb", false)

	if _, err := s.Session(ctx(), "old"); !errors.Is(err, store.ErrNotFound) {
		t.Error("the oldest session was not evicted")
	}
	if s.Dropped() != 1 {
		t.Errorf("Dropped = %d, want 1", s.Dropped())
	}

	page, _ := s.Events(ctx(), store.EventFilter{SessionID: "old"})
	if len(page.Events) != 0 {
		t.Errorf("%d orphaned events survived their session", len(page.Events))
	}
	if _, err := s.Session(ctx(), "new"); err != nil {
		t.Error("the newest session was evicted")
	}
}

func TestConcurrentWritesLoseNothing(t *testing.T) {
	s := store.NewMemoryStore(1000)
	const writers, each = 8, 50

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range each {
				_ = s.Write(ctx(), audit.Event{
					Kind: audit.KindStatement, Timestamp: time.Now().UTC(),
					SessionID: session.ID("w" + string(rune('a'+w))),
					Principal: "alice", Operation: hoopinspect.OpSelect,
					Statement: "SELECT " + string(rune('0'+i%10)), Allowed: true,
				})
			}
		}(w)
	}
	wg.Wait()

	page, err := s.Events(ctx(), store.EventFilter{Limit: store.MaxLimit})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if page.Total != writers*each {
		t.Errorf("Total = %d, want %d", page.Total, writers*each)
	}
}

func TestContextCancellationIsHonored(t *testing.T) {
	s := store.NewMemoryStore(10)
	seed(t, s, "a", "alice", "appdb", false)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.Sessions(cancelled, store.SessionFilter{}); err == nil {
		t.Error("Sessions ignored a cancelled context")
	}
	if _, err := s.Events(cancelled, store.EventFilter{}); err == nil {
		t.Error("Events ignored a cancelled context")
	}
	if _, err := s.Stats(cancelled, store.SessionFilter{}); err == nil {
		t.Error("Stats ignored a cancelled context")
	}
}

func TestLimitIsClamped(t *testing.T) {
	f := store.SessionFilter{Limit: store.MaxLimit + 5000}.Normalize()
	if f.Limit != store.MaxLimit {
		t.Errorf("Limit = %d, want clamped to %d", f.Limit, store.MaxLimit)
	}
	var ef store.EventFilter
	if ef.Normalize().Limit != store.DefaultLimit {
		t.Error("zero limit was not defaulted")
	}
}

func TestClassifyVerdictPrecedence(t *testing.T) {
	// A session that was refused AND then failed matters first for the
	// refusal.
	if got := store.ClassifyVerdict(1, 1); got != store.VerdictDenied {
		t.Errorf("ClassifyVerdict(1,1) = %q, want denied", got)
	}
	if got := store.ClassifyVerdict(0, 1); got != store.VerdictError {
		t.Errorf("ClassifyVerdict(0,1) = %q, want error", got)
	}
	if got := store.ClassifyVerdict(0, 0); got != store.VerdictClean {
		t.Errorf("ClassifyVerdict(0,0) = %q, want clean", got)
	}
}

// --- HTTP API ------------------------------------------------------------

func newAPI(t *testing.T) (*store.MemoryStore, http.Handler) {
	t.Helper()
	s := store.NewMemoryStore(100)
	seed(t, s, "a", "alice", "appdb", true)
	seed(t, s, "b", "bob", "api", false)
	return s, store.NewAPI(s).Routes()
}

func get(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))

	var body map[string]any
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
	}
	return rr, body
}

func TestAPIListSessions(t *testing.T) {
	_, h := newAPI(t)
	rr, body := get(t, h, "/sessions")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	sessions, _ := body["sessions"].([]any)
	if len(sessions) != 2 {
		t.Errorf("got %d sessions", len(sessions))
	}
	if body["total"].(float64) != 2 {
		t.Errorf("total = %v", body["total"])
	}
}

func TestAPIFilters(t *testing.T) {
	_, h := newAPI(t)

	_, body := get(t, h, "/sessions?principal=alice")
	if n := len(body["sessions"].([]any)); n != 1 {
		t.Errorf("principal filter returned %d", n)
	}

	_, body = get(t, h, "/sessions?denied=true")
	if n := len(body["sessions"].([]any)); n != 1 {
		t.Errorf("denied filter returned %d", n)
	}
}

// A filter that drops a typo shows the operator the wrong window with no
// signal that it happened.
func TestAPIRejectsUnparseableFilters(t *testing.T) {
	_, h := newAPI(t)
	for _, path := range []string{
		"/sessions?since=yesterdy",
		"/sessions?denied=maybe",
		"/sessions?limit=abc",
		"/sessions?limit=-1",
	} {
		rr, _ := get(t, h, path)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, rr.Code)
		}
	}
}

func TestAPIGetSessionAndNotFound(t *testing.T) {
	_, h := newAPI(t)

	rr, body := get(t, h, "/sessions/a")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if body["principal"] != "alice" {
		t.Errorf("principal = %v", body["principal"])
	}

	rr, _ = get(t, h, "/sessions/nope")
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing session: status = %d, want 404", rr.Code)
	}
}

// The path segment must win, or /sessions/A/events?session_id=B leaks B.
func TestAPISessionEventsIgnoresConflictingQueryParam(t *testing.T) {
	_, h := newAPI(t)
	_, body := get(t, h, "/sessions/a/events?session_id=b")

	events, _ := body["events"].([]any)
	if len(events) == 0 {
		t.Fatal("no events returned")
	}
	for _, e := range events {
		if id := e.(map[string]any)["session_id"]; id != "a" {
			t.Errorf("event from session %v leaked into session a's timeline", id)
		}
	}
}

func TestAPIStats(t *testing.T) {
	_, h := newAPI(t)
	rr, body := get(t, h, "/stats")

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if body["sessions"].(float64) != 2 {
		t.Errorf("sessions = %v", body["sessions"])
	}
	if body["denied"].(float64) != 1 {
		t.Errorf("denied = %v", body["denied"])
	}
}

// Audit data is full of SQL. Escaping > into \u003e makes it unreadable in a
// terminal and unsearchable in devtools.
func TestAPIDoesNotEscapeHTMLInStatements(t *testing.T) {
	s := store.NewMemoryStore(10)
	s.Write(ctx(), audit.Event{
		Kind: audit.KindStatement, Timestamp: time.Now().UTC(), SessionID: "x",
		Principal: "alice", Operation: hoopinspect.OpSelect,
		Statement: "SELECT * FROM t WHERE age > 30 AND a & b", Allowed: true,
	})

	rr := httptest.NewRecorder()
	store.NewAPI(s).Routes().ServeHTTP(rr, httptest.NewRequest("GET", "/events", nil))

	if strings.Contains(rr.Body.String(), `\u003e`) {
		t.Error("'>' was escaped; audit output must stay greppable")
	}
	if !strings.Contains(rr.Body.String(), "age > 30") {
		t.Errorf("statement not readable in output: %s", rr.Body.String())
	}
}

func TestAPIBasePathStripping(t *testing.T) {
	s := store.NewMemoryStore(10)
	seed(t, s, "a", "alice", "appdb", false)

	api := store.NewAPI(s)
	api.BasePath = "/audit"

	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, httptest.NewRequest("GET", "/audit/sessions", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("mounted under a base path: status = %d", rr.Code)
	}
}
