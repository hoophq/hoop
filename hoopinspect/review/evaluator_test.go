package review

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/analyzer"
	"github.com/hoophq/hoopinspect/policy"
)

// fakeGateway answers claim and create independently and counts every call, so
// a test can assert that a round trip did NOT happen — which is most of what
// matters here.
type fakeGateway struct {
	*httptest.Server

	claims  atomic.Int64
	creates atomic.Int64

	// approved is the hash the gateway holds an approval for. Consumed on
	// the first claim, exactly as the real single-use claim is.
	approved atomic.Value // string

	fail atomic.Bool
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	g := &fakeGateway{}
	g.approved.Store("")
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.fail.Load() {
			http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case claimPath:
			g.claims.Add(1)
			if held, _ := g.approved.Load().(string); held != "" && held == body["statement_hash"] {
				g.approved.Store("") // single use
				_ = json.NewEncoder(w).Encode(Ticket{
					ReviewID: "rev-approved", SessionID: "s1", Status: "EXECUTED",
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "no approval"})
		case createPath:
			g.creates.Add(1)
			// A distinct review per request, keyed on the marker, as the real
			// create path does. Unmarked requests keep the plain ids so the
			// assertions elsewhere in this file read unchanged.
			id, sid := "rev-pending", "s2"
			if m := body["marker"]; m != "" {
				id, sid = "rev-"+m, "s-"+m
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(Ticket{
				ReviewID: id, SessionID: sid, Status: "PENDING",
				URL: "https://gw/sessions/" + sid,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *fakeGateway) gate(t *testing.T, opts Options) *Gate {
	t.Helper()
	c, err := NewClient(g.URL, "hpk_test", time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	opts.Client = c
	if opts.Triggers == nil {
		opts.Triggers = []analyzer.Trigger{{Operations: []hoopinspect.Operation{hoopinspect.OpDelete}}}
	}
	rg, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return rg
}

// del builds the statement a real postgres simple Query produces, metadata
// included: the gate keys observability on the message kind, so a statement
// without it is not the thing the codec hands over.
func del(text string) hoopinspect.Statement {
	return hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Direction: hoopinspect.FromClient,
		Text:      text,
		Operation: hoopinspect.OpDelete,
		Tables:    []string{"users"},
		Metadata:  map[string]string{"pg.message": "Query"},
	}
}

// sel is a statement no configured trigger matches, so the claim phase must
// not spend a round trip on it.
func sel() hoopinspect.Statement {
	return hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Direction: hoopinspect.FromClient,
		Text:      "SELECT 1",
		Operation: hoopinspect.OpSelect,
		Metadata:  map[string]string{"pg.message": "Query"},
	}
}

func newCtx() *policy.EvalContext {
	return &policy.EvalContext{Context: map[string]string{"connection": "appdb"}}
}

// gatedCtx is what the chain looks like when the analyzer has flagged the
// statement: an answered ai_analysis finding whose action is require_review.
func gatedCtx() *policy.EvalContext {
	ec := newCtx()
	ec.AddFinding(policy.Finding{
		Source: analyzer.Source,
		Rule:   "risky-writes",
		Status: policy.FindingOK,
		Values: map[string]any{
			analyzer.FindingRiskLevel: "high",
			analyzer.FindingAction:    string(analyzer.ActionRequireReview),
		},
	})
	return ec
}

// The claim must not spend a gateway round-trip on a statement the analyzer
// would never classify: on a busy lane that is a network call per query.
func TestClaimSkipsNonCandidates(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})

	stmt := sel()
	if v := rg.Claim().EvaluateWith(stmt, newCtx()); v.Denied {
		t.Fatal("a non-candidate was denied")
	}
	if n := g.claims.Load(); n != 0 {
		t.Errorf("claims = %d, want 0", n)
	}
}

// A gate-phase policy that named the analyzer overrides the YAML trigger in
// BOTH directions, exactly as it does for the analyzer itself. Otherwise a
// lane that moved the decision into Rego would find the claim still narrowing
// by a trigger the policy replaced.
func TestClaimHonoursThePolicysRequest(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})

	ec := newCtx()
	ec.Requested = map[string]bool{analyzer.Source: true}
	rg.Claim().EvaluateWith(sel(), ec)
	if n := g.claims.Load(); n != 1 {
		t.Errorf("a requested statement was not claimed for: claims = %d", n)
	}

	ec = newCtx()
	ec.Requested = map[string]bool{analyzer.Source: false}
	rg.Claim().EvaluateWith(del("DELETE FROM users WHERE id = 7"), ec)
	if n := g.claims.Load(); n != 1 {
		t.Errorf("a vetoed statement was claimed for anyway: claims = %d", n)
	}
}

// The whole point of running the claim before the analyzer: an approved retry
// costs one gateway call and no model call.
func TestApprovedStatementProceedsAndVetoesTheAnalyzer(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})
	stmt := del("DELETE FROM users WHERE id = 7")
	id := identify(stmt, "")
	g.approved.Store(id.Hash)

	ec := newCtx()
	v := rg.Claim().EvaluateWith(stmt, ec)
	if v.Denied {
		t.Fatalf("an approved statement was denied: %q", v.Message)
	}
	if want, stated := ec.WantsRun(analyzer.Source); !stated || want {
		t.Error("the analyzer was not vetoed; an approved retry must not be reclassified")
	}
	if v.Annotations[AnnotationStatus] != StatusApproved {
		t.Errorf("annotations = %v", v.Annotations)
	}

	// Single use. The second attempt finds nothing and falls through to the
	// analyzer, which is what makes the gateway's atomic claim observable
	// from here.
	ec2 := newCtx()
	rg.Claim().EvaluateWith(stmt, ec2)
	if _, stated := ec2.WantsRun(analyzer.Source); stated {
		t.Error("a consumed approval was reused")
	}
}

// A different statement must never ride on an approval. This is the threat the
// exact hash exists for.
func TestApprovalDoesNotCoverADifferentStatement(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})
	g.approved.Store(identify(del("DELETE FROM users WHERE id = 1"), "").Hash)

	ec := newCtx()
	rg.Claim().EvaluateWith(del("DELETE FROM users WHERE id = 999"), ec)
	if _, stated := ec.WantsRun(analyzer.Source); stated {
		t.Fatal("an approval for id = 1 authorized id = 999")
	}
}

// A gateway blip at claim time must not break statements that were never
// gated: the analyzer has not run yet, so nothing has established that this
// statement needs a human at all.
func TestClaimNeverDenies(t *testing.T) {
	g := newFakeGateway(t)
	g.fail.Store(true)
	rg := g.gate(t, Options{})

	v := rg.Claim().EvaluateWith(del("DELETE FROM users WHERE id = 7"), newCtx())
	if v.Denied {
		t.Fatal("a gateway failure denied a statement before it was known to be gated")
	}
	if v.Err == nil {
		t.Error("the failure was swallowed; the trail must show the gate could not check")
	}
}

// The decide phase acts only on the analyzer's verdict. A statement nothing
// flagged is not this evaluator's business.
func TestDecideIgnoresUnflaggedStatements(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})

	for _, ec := range []*policy.EvalContext{
		newCtx(), // no finding at all
		func() *policy.EvalContext { // flagged, but mapped to warn
			ec := newCtx()
			ec.AddFinding(policy.Finding{
				Source: analyzer.Source, Status: policy.FindingOK,
				Values: map[string]any{
					analyzer.FindingRiskLevel: "high",
					analyzer.FindingAction:    string(analyzer.ActionWarn),
				},
			})
			return ec
		}(),
		func() *policy.EvalContext { // the analyzer could not answer
			ec := newCtx()
			ec.AddFinding(policy.Finding{Source: analyzer.Source, Status: policy.FindingError})
			return ec
		}(),
	} {
		if v := rg.Decide().EvaluateWith(del("DELETE FROM users"), ec); v.Denied {
			t.Errorf("denied an unflagged statement: %q", v.Message)
		}
	}
	if n := g.creates.Load(); n != 0 {
		t.Errorf("creates = %d, want 0", n)
	}
}

func TestDecideFilesAReviewAndRefusesWithTheURL(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})

	v := rg.Decide().EvaluateWith(del("DELETE FROM users WHERE id = 7"), gatedCtx())
	if !v.Denied {
		t.Fatal("a flagged statement was forwarded")
	}
	if !strings.Contains(v.Message, "https://gw/sessions/s2") {
		t.Errorf("the denial does not carry the review url: %q", v.Message)
	}
	if !strings.Contains(v.Message, "reconnect") {
		t.Errorf("the denial does not tell the agent what to do next: %q", v.Message)
	}
	if v.Rule != Rule {
		t.Errorf("rule = %q, want %q", v.Rule, Rule)
	}
	if v.Annotations[AnnotationStatus] != StatusPending || v.Annotations[AnnotationID] != "rev-pending" {
		t.Errorf("annotations = %v", v.Annotations)
	}
	if n := g.creates.Load(); n != 1 {
		t.Errorf("creates = %d, want 1", n)
	}
}

// A Parse is refused WITHOUT filing anything: an approval for a statement
// whose parameters the gate cannot read would cover every later binding, so
// there is nothing safe to ask a human.
func TestDecideRefusesTheExtendedProtocolWithoutFiling(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})

	stmt := del("DELETE FROM users WHERE id = $1")
	stmt.Metadata = map[string]string{"pg.message": "Parse"}

	v := rg.Decide().EvaluateWith(stmt, gatedCtx())
	if !v.Denied {
		t.Fatal("a Parse under a review gate was forwarded")
	}
	if v.Annotations[AnnotationStatus] != StatusRefused {
		t.Errorf("status = %q, want refused", v.Annotations[AnnotationStatus])
	}
	if n := g.creates.Load(); n != 0 {
		t.Errorf("a review was filed for an unapprovable statement: creates = %d", n)
	}
}

func TestRequireMarkerRefusesAnUnmarkedStatement(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{RequireMarker: true})

	v := rg.Decide().EvaluateWith(del("DELETE FROM users WHERE id = 7"), gatedCtx())
	if !v.Denied || v.Annotations[AnnotationStatus] != StatusRefused {
		t.Fatalf("an unmarked statement was not refused: %+v", v)
	}
	if n := g.creates.Load(); n != 0 {
		t.Errorf("creates = %d, want 0", n)
	}

	marked := del("-- x-hoop-correlation-id=task-42\nDELETE FROM users WHERE id = 7")
	if v := rg.Decide().EvaluateWith(marked, gatedCtx()); v.Annotations[AnnotationStatus] != StatusPending {
		t.Errorf("a marked statement was refused: %+v", v)
	}
}

// An unreachable gateway refuses. A review gate that forwards when its backend
// is down is not a review gate.
func TestDecideFailsClosed(t *testing.T) {
	g := newFakeGateway(t)
	g.fail.Store(true)
	rg := g.gate(t, Options{})

	v := rg.Decide().EvaluateWith(del("DELETE FROM users WHERE id = 7"), gatedCtx())
	if !v.Denied {
		t.Fatal("an unreachable gateway forwarded a gated statement")
	}
	if v.Annotations[AnnotationStatus] != StatusUnavailable {
		t.Errorf("status = %q, want unavailable", v.Annotations[AnnotationStatus])
	}
	if v.Err == nil {
		t.Error("the cause is missing from the verdict")
	}
}

// A lane with no connection name cannot scope an approval, so it refuses
// rather than asking the gateway a question with no answer.
func TestDecideRefusesWithoutAConnectionName(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})

	ec := gatedCtx()
	delete(ec.Context, "connection")

	v := rg.Decide().EvaluateWith(del("DELETE FROM users"), ec)
	if !v.Denied || g.creates.Load() != 0 {
		t.Fatalf("verdict = %+v, creates = %d", v, g.creates.Load())
	}
}

// A polling agent must not turn one refusal into a stream of gateway calls.
func TestPendingIsCachedButApprovedIsNot(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{PendingTTL: time.Minute})
	stmt := del("DELETE FROM users WHERE id = 7")

	for range 3 {
		if v := rg.Decide().EvaluateWith(stmt, gatedCtx()); !v.Denied {
			t.Fatal("a cached pending answer forwarded the statement")
		}
	}
	if n := g.creates.Load(); n != 1 {
		t.Errorf("creates = %d, want 1: the pending answer was not cached", n)
	}
	// While a refusal is cached the claim is skipped too, since the answer
	// cannot have been approved without the cache being wrong in the safe
	// direction.
	if n := g.claims.Load(); n != 0 {
		t.Errorf("claims = %d, want 0", n)
	}

	// An approval landing later must still be honoured once the negative
	// entry expires — the cache holds refusals only, never approvals.
	rg.forgetPending("appdb", identify(stmt, "").Hash, identify(stmt, "").Marker)
	g.approved.Store(identify(stmt, "").Hash)
	ec := newCtx()
	rg.Claim().EvaluateWith(stmt, ec)
	if want, stated := ec.WantsRun(analyzer.Source); !stated || want {
		t.Error("an approval was not honoured after the negative entry was dropped")
	}
}

// Responses are never gated: whatever a response reports has already happened
// upstream, so there is nothing left to approve.
func TestResponsesAreIgnored(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})

	stmt := del("DELETE FROM users")
	stmt.Direction = hoopinspect.FromServer

	if v := rg.Decide().EvaluateWith(stmt, gatedCtx()); v.Denied {
		t.Fatal("a response was denied")
	}
	if n := g.creates.Load(); n != 0 {
		t.Errorf("creates = %d, want 0", n)
	}
}

// stubAnalyzer stands in for analyzer.Evaluator: it flags whatever it is
// shown as require_review, unless the claim vetoed it. That veto contract is
// the only part of the real analyzer this gate depends on, so it is the part
// worth reproducing here rather than linking a provider.
type stubAnalyzer struct{}

func (stubAnalyzer) Evaluate(hoopinspect.Statement) policy.Verdict { return policy.Verdict{} }

func (stubAnalyzer) EvaluateWith(_ hoopinspect.Statement, ec *policy.EvalContext) policy.Verdict {
	if want, stated := ec.WantsRun(analyzer.Source); stated && !want {
		ec.AddFinding(policy.Finding{Source: analyzer.Source, Status: policy.FindingSkipped})
		return policy.Verdict{}
	}
	ec.AddFinding(policy.Finding{
		Source: analyzer.Source, Rule: "risky-writes", Status: policy.FindingOK,
		Values: map[string]any{
			analyzer.FindingRiskLevel: "high",
			analyzer.FindingAction:    string(analyzer.ActionRequireReview),
		},
	})
	return policy.Verdict{}
}

// The two phases compose in a Chain the way buildPolicy assembles them, and
// the outcome flips on nothing but whether the gateway holds an approval.
func TestChainEndToEnd(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{})
	stmt := del("DELETE FROM users WHERE id = 7")

	// A stand-in for the analyzer: flags whatever it is shown, unless the
	// claim vetoed it. It is the same contract the real one honours.
	chain := policy.Chain{rg.Claim(), stubAnalyzer{}, rg.Decide()}

	v := chain.EvaluateWith(stmt, newCtx())
	if !v.Denied {
		t.Fatal("the first attempt was not refused")
	}

	g.approved.Store(identify(stmt, "").Hash)
	if v := chain.EvaluateWith(stmt, newCtx()); v.Denied {
		t.Fatalf("the approved retry was refused: %q", v.Message)
	}
	if n := g.creates.Load(); n != 1 {
		t.Errorf("creates = %d, want 1: the approved retry filed a second review", n)
	}
}

// The regression a live HTTP lane hit: a top-level ai rule that does not match
// plus a lane rule that does, and the gate forwarded the request. The finding
// is the gate's only input, so a fold that dropped the classification made it
// allow a statement the analyzer had just rated high.
//
// Driven through REAL analyzer evaluators rather than hand-built findings,
// because the bug was in how the analyzer folds its own rules.
func TestGatedWhenAnotherRuleWasSkipped(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{Triggers: []analyzer.Trigger{{Resources: []string{"/anything/**"}}}})

	stmt := hoopinspect.Statement{
		Protocol: hoopinspect.HTTP, Direction: hoopinspect.FromClient,
		Text: "POST /anything/users/12345/orders",
		HTTP: &hoopinspect.HTTPDetail{Method: "POST", Path: "/anything/users/12345/orders",
			Resource: "/anything/users/*/orders", Body: `{"action":"purge"}`},
	}

	mk := func(rule string, trig analyzer.Trigger) *analyzer.Evaluator {
		ev, err := analyzer.New(analyzer.Config{
			Rule: rule, Provider: highRiskProvider{}, Trigger: trig,
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
		})
		if err != nil {
			t.Fatalf("analyzer.New: %v", err)
		}
		return ev
	}
	matches := mk("risky-payloads", analyzer.Trigger{Resources: []string{"/anything/**"}})
	doesNot := mk("risky-writes", analyzer.Trigger{Operations: []hoopinspect.Operation{hoopinspect.OpDelete}})

	// Either order: enforcement must not depend on which rule ran first.
	for _, order := range [][]*analyzer.Evaluator{{matches, doesNot}, {doesNot, matches}} {
		ec := newCtx()
		for _, ev := range order {
			ev.EvaluateWith(stmt, ec)
		}
		if v := rg.Decide().EvaluateWith(stmt, ec); !v.Denied {
			t.Errorf("%s ran first: request forwarded; finding was %+v",
				order[0].Rule(), ec.Findings[analyzer.Source])
		}
	}
}

// highRiskProvider rates everything high without leaving the process.
type highRiskProvider struct{}

func (highRiskProvider) Name() string { return "stub" }
func (highRiskProvider) Classify(context.Context, string, string) (*analyzer.Result, error) {
	return &analyzer.Result{RiskLevel: analyzer.RiskHigh, Title: "dangerous"}, nil
}

// The refusal a caller reads has to describe a mechanism their protocol has.
// This is the exact 403 body an HTTP client gets, which is where a SQL-shaped
// instruction was reaching people who could not act on it.
func TestRequireMarkerRefusalIsProtocolAppropriate(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{RequireMarker: true})

	httpStmt := hoopinspect.Statement{
		Protocol:  hoopinspect.HTTP,
		Direction: hoopinspect.FromClient,
		Text:      "POST /anything/users/12345/orders",
		HTTP:      &hoopinspect.HTTPDetail{Body: `{"action":"purge"}`},
	}
	v := rg.Decide().EvaluateWith(httpStmt, gatedCtx())
	if !v.Denied {
		t.Fatal("an unmarked HTTP request was forwarded")
	}
	t.Logf("HTTP caller reads: %s", v.Message)
	if strings.Contains(v.Message, "--") {
		t.Errorf("HTTP refusal tells the caller to use a SQL comment:\n  %s", v.Message)
	}
	if !strings.Contains(v.Message, hoopinspect.CorrelationHeader) {
		t.Errorf("HTTP refusal does not name the header:\n  %s", v.Message)
	}

	// The SQL lane keeps its own advice.
	sqlVerdict := rg.Decide().EvaluateWith(del("DELETE FROM users WHERE id = 7"), gatedCtx())
	t.Logf("SQL caller reads: %s", sqlVerdict.Message)
	if !strings.Contains(sqlVerdict.Message, "-- x-hoop-correlation-id=") {
		t.Errorf("SQL refusal lost the comment form:\n  %s", sqlVerdict.Message)
	}

	// And the header actually satisfies require_marker, rather than the
	// lane being unusable with it on.
	httpStmt.HTTP.CorrelationID = "task-1"
	if v := rg.Decide().EvaluateWith(httpStmt, gatedCtx()); v.Annotations[AnnotationStatus] != StatusPending {
		t.Errorf("a marked HTTP request was refused: status=%q reason=%s",
			v.Annotations[AnnotationStatus], v.Message)
	}
}

// Two tasks running byte-identical SQL are two requests, each needing its own
// human — which is why the gateway dedupes on the marker rather than on the
// statement. The sidecar's negative cache must not collapse them back
// together: a hash-only key handed the second task the first one's ticket,
// pointed it at a review it never filed, and filed none of its own until the
// entry expired.
func TestPendingCacheDoesNotCollapseDistinctMarkers(t *testing.T) {
	g := newFakeGateway(t)
	rg := g.gate(t, Options{PendingTTL: time.Minute})

	const sql = "DELETE FROM users WHERE id = 7"
	first := rg.Decide().EvaluateWith(del("-- x-hoop-correlation-id=task-3\n"+sql), gatedCtx())
	second := rg.Decide().EvaluateWith(del("-- x-hoop-correlation-id=task-9\n"+sql), gatedCtx())

	if n := g.creates.Load(); n != 2 {
		t.Fatalf("creates = %d, want 2 — the second task's review was never filed", n)
	}
	if !first.Denied || !second.Denied {
		t.Fatal("a gated statement was forwarded")
	}

	firstID := first.Annotations[AnnotationID]
	secondID := second.Annotations[AnnotationID]
	if firstID == secondID {
		t.Errorf("both tasks were given review %q; the second was handed the first's ticket", firstID)
	}
	if secondID != "rev-task-9" {
		t.Errorf("second task got review %q, want its own (rev-task-9)", secondID)
	}
	if !strings.Contains(second.Message, "s-task-9") {
		t.Errorf("the second task was pointed at the wrong review:\n  %s", second.Message)
	}
}

// The cache still has to do its job. A genuine retry — the same marker, the
// same statement — is what it exists to absorb, and an unmarked caller has to
// keep collapsing onto one entry: with no marker the gateway files a review
// per attempt, so this cache is all that stands between a hot retry loop and a
// queue full of duplicates.
func TestPendingCacheStillAbsorbsRetries(t *testing.T) {
	for _, tc := range []struct{ name, prefix string }{
		{"same marker", "-- x-hoop-correlation-id=task-3\n"},
		{"no marker", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGateway(t)
			rg := g.gate(t, Options{PendingTTL: time.Minute})
			stmt := del(tc.prefix + "DELETE FROM users WHERE id = 7")

			for range 5 {
				if v := rg.Decide().EvaluateWith(stmt, gatedCtx()); !v.Denied {
					t.Fatal("a gated statement was forwarded")
				}
			}
			if n := g.creates.Load(); n != 1 {
				t.Errorf("creates = %d, want 1 — the cache stopped absorbing retries", n)
			}
		})
	}
}
