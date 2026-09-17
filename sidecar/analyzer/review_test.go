package analyzer_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// recordingReviewer stands in for the control plane. It records what was sent
// so a test can assert on the bytes rather than on the call count alone: the
// backend matches an approval against those bytes, so "it was called" is not
// the property that matters.
type recordingReviewer struct {
	mu   sync.Mutex
	sent []string

	res analyzer.ReviewResult
	err error

	// block holds the call until ctx is done, for the timeout case.
	block bool
}

func (r *recordingReviewer) review(ctx context.Context, statement string) (analyzer.ReviewResult, error) {
	r.mu.Lock()
	r.sent = append(r.sent, statement)
	r.mu.Unlock()
	if r.block {
		<-ctx.Done()
		return analyzer.ReviewResult{}, ctx.Err()
	}
	return r.res, r.err
}

func (r *recordingReviewer) statements() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sent...)
}

// holdingEvaluator maps high risk to a hold, which is the only configuration
// under test in this file.
func holdingEvaluator(t *testing.T, rev *recordingReviewer, edit func(*analyzer.Config)) *analyzer.Evaluator {
	t.Helper()
	cfg := analyzer.Config{
		Rule:     "payments",
		Provider: &stubProvider{level: analyzer.RiskHigh},
		Trigger:  deleteTrigger(),
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
	}
	if rev != nil {
		cfg.Review = rev.review
	}
	if edit != nil {
		edit(&cfg)
	}
	return mustNew(t, cfg)
}

func deleteStatement() inspect.Statement {
	return sqlStmt("DELETE FROM users WHERE email = 'a@b.c'", inspect.OpDelete, "users")
}

// The release: the plane consumed an approved review for these exact bytes,
// so the statement travels and the record names the review that let it.
func TestAnApprovedReviewForwardsTheStatement(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{
		Forward: true, ID: "9f97", Status: "EXECUTED",
	}}
	v := holdingEvaluator(t, rev, nil).Evaluate(deleteStatement())

	if v.Denied {
		t.Fatalf("a consumed approval did not forward: %q", v.Message)
	}
	if got := v.Annotations[analyzer.MetadataReviewID]; got != "9f97" {
		t.Errorf("the audit record carries review id %q, want 9f97", got)
	}
	if got := v.Annotations[analyzer.MetadataAction]; got != string(analyzer.ActionRequireReview) {
		t.Errorf("risk_action is %q, want require_review", got)
	}
}

// Every answer that is not a release denies, and the id has to reach the
// developer: it is the only thing they can give an approver to find the
// request. The wording separates "wait" from "stop", because a rejection is
// permanent and a retry will never clear it.
func TestAnUnreleasedReviewDeniesAndNamesIt(t *testing.T) {
	for _, tc := range []struct {
		name, status, want string
	}{
		{"pending", "PENDING", "waiting for approval"},
		{"rejected", "REJECTED", "was rejected"},
		{"revoked", "REVOKED", "was revoked"},
		{"claim lost", "EXECUTED", "already used"},
		{"unknown status", "SOMETHING_NEW", "not released"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: tc.status}}
			v := holdingEvaluator(t, rev, nil).Evaluate(deleteStatement())

			if !v.Denied {
				t.Fatal("a statement with no approval was forwarded")
			}
			if !strings.Contains(v.Message, "9f97") {
				t.Errorf("the denial does not carry the review id: %q", v.Message)
			}
			if !strings.Contains(v.Message, tc.want) {
				t.Errorf("denial %q does not say %q", v.Message, tc.want)
			}
			if v.Source != policy.SourceAnalyzer || v.Rule != "payments" {
				t.Errorf("denial is attributed to %q/%q, want analyzer/payments", v.Source, v.Rule)
			}
		})
	}
}

// fail_open is an answer about a model vendor's outage, not about a human
// gate. A lane that holds and cannot reach its backend denies whatever the
// setting says, or every outage becomes a way around the approval.
func TestAFailedReviewDeniesUnderFailOpen(t *testing.T) {
	rev := &recordingReviewer{err: errors.New("the control plane is unreachable")}
	ev := holdingEvaluator(t, rev, func(c *analyzer.Config) { c.FailOpen = true })

	v := ev.Evaluate(deleteStatement())
	if !v.Denied {
		t.Fatal("a failed review forwarded the statement because fail_open was set")
	}
	if v.Err == nil {
		t.Error("the verdict carries no error, so the trail cannot say why")
	}
}

// No backend is the observed lane and the lane that never reached a plane.
// Both deny, and neither pretends a review exists.
func TestNoReviewBackendDenies(t *testing.T) {
	ev := holdingEvaluator(t, nil, func(c *analyzer.Config) { c.FailOpen = true })

	v := ev.Evaluate(deleteStatement())
	if !v.Denied {
		t.Fatal("a hold with no review backend forwarded the statement")
	}
	if strings.Contains(v.Message, "(review ") {
		t.Errorf("the denial quotes a review id that was never filed: %q", v.Message)
	}
}

// The cache collapses CLASSIFICATIONS. An approval is not a classification:
// it is spent on one statement, so the second one of the same shape has to
// ask again even though the model does not.
func TestACachedVerdictStillFilesAReview(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskHigh}
	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}}
	ev := mustNew(t, analyzer.Config{
		Rule:      "payments",
		Provider:  p,
		Trigger:   deleteTrigger(),
		Actions:   analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
		Review:    rev.review,
		CacheSize: 16,
		CacheTTL:  time.Minute,
	})

	ev.Evaluate(deleteStatement())
	ev.Evaluate(deleteStatement())

	if calls := p.calls.Load(); calls != 1 {
		t.Errorf("the provider was called %d times, want 1: the cache should have hit", calls)
	}
	if sent := rev.statements(); len(sent) != 2 {
		t.Errorf("the review was filed %d times, want 2: a cache hit is not an approval", len(sent))
	}
}

// What the plane hashes has to be what the client sends, so the review gets
// the raw statement: not the redacted rendering a model vendor sees, and not
// the truncated one either.
func TestTheReviewSendsTheRawStatement(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}}
	ev := holdingEvaluator(t, rev, func(c *analyzer.Config) {
		c.MaxInputBytes = 12
		c.Redact = func(string) string { return "REDACTED" }
	})

	stmt := deleteStatement()
	ev.Evaluate(stmt)

	sent := rev.statements()
	if len(sent) != 1 {
		t.Fatalf("the review was filed %d times, want 1", len(sent))
	}
	if sent[0] != stmt.Text {
		t.Errorf("the review carries %q, want the raw statement %q", sent[0], stmt.Text)
	}
}

// A hold sits inline on a proxied connection, so it is bounded by the same
// timeout the model call is. Without the deadline a plane that never answers
// would hold the client's socket until something else gave up.
func TestTheReviewIsBoundedByTheTimeout(t *testing.T) {
	rev := &recordingReviewer{block: true}
	ev := holdingEvaluator(t, rev, func(c *analyzer.Config) {
		c.Timeout = 50 * time.Millisecond
		c.FailOpen = true
	})

	start := time.Now()
	v := ev.Evaluate(deleteStatement())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the hold took %v, the timeout was 50ms", elapsed)
	}
	if !v.Denied {
		t.Error("a review that never answered forwarded the statement")
	}
}

// A spent budget means no classification, and on a holding lane no
// classification cannot mean forward: max_calls is a cost bound, and a cost
// bound must not retire a human gate.
func TestASpentBudgetDeniesOnAHoldingLane(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}}
	ev := holdingEvaluator(t, rev, func(c *analyzer.Config) {
		c.MaxCalls = 1
		c.FailOpen = true
	})

	ev.Evaluate(deleteStatement())
	v := ev.Evaluate(sqlStmt("DELETE FROM orders", inspect.OpDelete, "orders"))

	if !v.Denied {
		t.Fatal("a statement past the budget was forwarded on a lane that holds")
	}
	if !strings.Contains(v.Message, "budget") {
		t.Errorf("the denial does not say the budget is why: %q", v.Message)
	}
	if sent := rev.statements(); len(sent) != 1 {
		t.Errorf("the review was filed %d times, want 1: nothing was classified the "+
			"second time, so there is nothing to review", len(sent))
	}
}

// A lane that does NOT hold keeps the old behaviour, so the fail-closed rule
// above is scoped to the lanes that asked for a human rather than a change to
// what max_calls means everywhere.
func TestASpentBudgetStillAllowsOnALaneThatDoesNotHold(t *testing.T) {
	ev := mustNew(t, analyzer.Config{
		Provider: &stubProvider{level: analyzer.RiskHigh},
		Trigger:  deleteTrigger(),
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
		MaxCalls: 1,
	})

	ev.Evaluate(deleteStatement())
	if v := ev.Evaluate(sqlStmt("DELETE FROM orders", inspect.OpDelete, "orders")); v.Denied {
		t.Errorf("a spent budget denied on a lane that only blocks: %q", v.Message)
	}
}

// On a GATED lane the trigger is empty and a gate-phase policy answers "is
// this worth classifying". A hold does not change who that question belongs
// to: Rego saying no means nothing was classified, so there is no level to
// hold on, exactly as an unmatched trigger means. An operator who moved that
// decision into their own policy keeps all of it.
func TestAGatePolicyDecidesWhetherAHoldRunsAtAll(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested bool
		filed     int
		denied    bool
	}{
		{"rego asks for the run", true, 1, true},
		{"rego vetoes the run", false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}}
			ev := mustNew(t, analyzer.Config{
				Rule:     "payments",
				Provider: &stubProvider{level: analyzer.RiskHigh},
				// No trigger: the gated lane's shape.
				Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
				Review:  rev.review,
			})

			ec := &policy.EvalContext{Requested: map[string]bool{analyzer.Source: tc.requested}}
			v := ev.EvaluateWith(deleteStatement(), ec)

			if v.Denied != tc.denied {
				t.Errorf("denied = %v, want %v (%q)", v.Denied, tc.denied, v.Message)
			}
			if got := len(rev.statements()); got != tc.filed {
				t.Errorf("the review was filed %d times, want %d", got, tc.filed)
			}
		})
	}
}

// The operator's message says what to do about the hold; the reason says what
// the statement is waiting on. Both reach the developer, because either one
// alone leaves them guessing.
func TestTheOperatorMessageSurvivesTheHold(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}}
	ev := holdingEvaluator(t, rev, func(c *analyzer.Config) {
		c.Message = "ask #dba before deleting from payments"
	})

	v := ev.Evaluate(deleteStatement())
	if !strings.Contains(v.Message, "ask #dba") {
		t.Errorf("the operator's message was dropped: %q", v.Message)
	}
	if !strings.Contains(v.Message, "9f97") {
		t.Errorf("the operator's message replaced the review id: %q", v.Message)
	}
}
