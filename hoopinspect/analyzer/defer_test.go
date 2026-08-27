package analyzer_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hoophq/hoop/hoopinspect"
	"github.com/hoophq/hoop/hoopinspect/analyzer"
	"github.com/hoophq/hoop/hoopinspect/policy"
)

// defer forwards the statement and leaves the level behind for whatever
// decides next. It is the whole mechanism by which a model verdict reaches an
// OPA policy: the annotation carries it into the trail, the finding carries it
// into the decide phase. Both have to be there even though nothing denied.
func TestDeferForwardsAndAnnotates(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskHigh}
	ev := mustNew(t, analyzer.Config{
		Rule:     "risky",
		Provider: p,
		Trigger:  deleteTrigger(),
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
	})

	ec := &policy.EvalContext{}
	v := ev.EvaluateWith(sqlStmt("DELETE FROM customers", hoopinspect.OpDelete, "customers"), ec)

	if v.Denied {
		t.Fatalf("defer denied instead of handing the decision on: %+v", v)
	}
	for key, want := range map[string]string{
		analyzer.MetadataRiskLevel: "high",
		analyzer.MetadataAction:    "defer",
		analyzer.MetadataAIStatus:  analyzer.StatusOK,
		analyzer.MetadataAIRule:    "risky",
	} {
		if got := v.Annotations[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	f, ok := ec.Finding(analyzer.Source)
	if !ok {
		t.Fatalf("defer left no finding for the decide phase: %+v", ec.Findings)
	}
	if f.Status != policy.FindingOK || f.Rule != "risky" {
		t.Errorf("finding = %+v, want status %q from rule risky", f, policy.FindingOK)
	}
	if got, _ := f.Values["risk_level"].(string); got != "high" {
		t.Errorf("finding risk_level = %q, want high", got)
	}
}

// A gate that asks for analysis overrides a trigger that would not have
// matched. Without this the trigger stays in YAML and the gate is decoration.
func TestGateRequestOverridesTheTrigger(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskHigh}
	ev := mustNew(t, analyzer.Config{
		Rule:     "risky",
		Provider: p,
		Trigger:  deleteTrigger(), // would not match a SELECT
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
	})

	ec := &policy.EvalContext{Requested: map[string]bool{analyzer.Source: true}}
	v := ev.EvaluateWith(sqlStmt("SELECT * FROM customers", hoopinspect.OpSelect, "customers"), ec)

	if p.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want the gate's request honored", p.calls.Load())
	}
	if !v.Denied {
		t.Errorf("the classified statement was not blocked: %+v", v)
	}
}

// The override runs both ways. A gate that says no must stop a call the
// trigger would have made, or an operator who moved the decision into Rego
// still pays for statements Rego told them to skip.
func TestGateVetoOverridesATriggerMatch(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskHigh}
	ev := mustNew(t, analyzer.Config{
		Rule:     "risky",
		Provider: p,
		Trigger:  deleteTrigger(), // matches
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
	})

	ec := &policy.EvalContext{Requested: map[string]bool{analyzer.Source: false}}
	v := ev.EvaluateWith(sqlStmt("DELETE FROM customers", hoopinspect.OpDelete, "customers"), ec)

	if p.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want the veto honored", p.calls.Load())
	}
	if v.Denied {
		t.Errorf("a vetoed statement was denied: %+v", v)
	}
	if got := v.Annotations[analyzer.MetadataAIStatus]; got != analyzer.StatusSkipped {
		t.Errorf("ai_status = %q, want %q", got, analyzer.StatusSkipped)
	}
}

// A context with no opinion is the no-gate case, which every lane was before
// this existed: the configured trigger decides and nothing changes.
func TestNoGateLeavesTheTriggerInCharge(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskHigh}
	ev := mustNew(t, analyzer.Config{
		Rule:     "risky",
		Provider: p,
		Trigger:  deleteTrigger(),
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
	})

	ev.EvaluateWith(sqlStmt("SELECT 1", hoopinspect.OpSelect), &policy.EvalContext{})
	if p.calls.Load() != 0 {
		t.Errorf("an unmatched trigger classified anyway: %d calls", p.calls.Load())
	}
	ev.EvaluateWith(sqlStmt("DELETE FROM t", hoopinspect.OpDelete, "t"), &policy.EvalContext{})
	if p.calls.Load() != 1 {
		t.Errorf("a matched trigger did not classify: %d calls", p.calls.Load())
	}
}

// Every reason the model did not answer needs its own status, because they
// all look identical to a policy reading only risk_level.
func TestStatusNamesWhyTheModelDidNotAnswer(t *testing.T) {
	deleteStmt := sqlStmt("DELETE FROM customers", hoopinspect.OpDelete, "customers")

	t.Run("trigger miss", func(t *testing.T) {
		ev := mustNew(t, analyzer.Config{
			Rule: "risky", Provider: &stubProvider{level: analyzer.RiskHigh},
			Trigger: deleteTrigger(),
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
		})
		v := ev.Evaluate(sqlStmt("SELECT 1", hoopinspect.OpSelect))
		if got := v.Annotations[analyzer.MetadataAIStatus]; got != analyzer.StatusSkipped {
			t.Errorf("ai_status = %q, want %q", got, analyzer.StatusSkipped)
		}
	})

	t.Run("provider error", func(t *testing.T) {
		ev := mustNew(t, analyzer.Config{
			Rule: "risky", Provider: &stubProvider{err: errors.New("upstream down")},
			Trigger: deleteTrigger(), FailOpen: true,
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
		})
		v := ev.Evaluate(deleteStmt)
		if got := v.Annotations[analyzer.MetadataAIStatus]; got != analyzer.StatusError {
			t.Errorf("ai_status = %q, want %q", got, analyzer.StatusError)
		}
		if v.Err == nil {
			t.Error("a fail-open error dropped the cause")
		}
	})

	t.Run("budget exhausted", func(t *testing.T) {
		ev := mustNew(t, analyzer.Config{
			Rule: "risky", Provider: &stubProvider{level: analyzer.RiskHigh},
			Trigger: deleteTrigger(), MaxCalls: 1,
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
		})
		ev.Evaluate(deleteStmt)
		v := ev.Evaluate(sqlStmt("DELETE FROM orders", hoopinspect.OpDelete, "orders"))
		if got := v.Annotations[analyzer.MetadataAIStatus]; got != analyzer.StatusBudget {
			t.Errorf("ai_status = %q, want %q", got, analyzer.StatusBudget)
		}
	})

	t.Run("refused before sending", func(t *testing.T) {
		ev := mustNew(t, analyzer.Config{
			Rule: "risky", Provider: &stubProvider{level: analyzer.RiskLow},
			Trigger: deleteTrigger(),
			Redact:  func(string) string { return analyzer.RefuseSentinel },
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
		})
		v := ev.Evaluate(deleteStmt)
		if !v.Denied {
			t.Fatal("send=refuse allowed the statement")
		}
		if got := v.Annotations[analyzer.MetadataAIStatus]; got != analyzer.StatusRefused {
			t.Errorf("ai_status = %q, want %q", got, analyzer.StatusRefused)
		}
	})

	t.Run("cached", func(t *testing.T) {
		ev := mustNew(t, analyzer.Config{
			Rule: "risky", Provider: &stubProvider{level: analyzer.RiskMedium},
			Trigger: deleteTrigger(), CacheSize: 8, CacheTTL: time.Minute,
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
		})
		ev.Evaluate(deleteStmt)
		v := ev.Evaluate(deleteStmt)
		if got := v.Annotations[analyzer.MetadataAIStatus]; got != analyzer.StatusCached {
			t.Errorf("ai_status = %q, want %q", got, analyzer.StatusCached)
		}
		if got := v.Annotations[analyzer.MetadataRiskLevel]; got != "medium" {
			t.Errorf("a cached verdict lost its level: %q", got)
		}
	})
}

// Every eligible evaluation reports a finding, and the level rides on it only
// where the model actually answered.
//
// The absence is the point. A policy reading only risk_level sees nothing
// under an outage, a spent budget or a trigger miss, and if it treats a
// missing level as "not risky" it fails open silently on all three. status is
// the key that separates "rated low" from "never rated", so it is the one a
// policy has to read first.
func TestFindingCarriesTheLevelOnlyWhenAnswered(t *testing.T) {
	deleteStmt := sqlStmt("DELETE FROM customers", hoopinspect.OpDelete, "customers")

	for _, tc := range []struct {
		name      string
		build     func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement)
		want      string
		wantLevel string
	}{
		{
			name: "answered",
			build: func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement) {
				return mustNew(t, analyzer.Config{
					Rule: "risky", Provider: &stubProvider{level: analyzer.RiskHigh},
					Trigger: deleteTrigger(),
					Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
				}), deleteStmt
			},
			want:      policy.FindingOK,
			wantLevel: "high",
		},
		{
			name: "answered from cache",
			build: func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement) {
				ev := mustNew(t, analyzer.Config{
					Rule: "risky", Provider: &stubProvider{level: analyzer.RiskMedium},
					Trigger: deleteTrigger(), CacheSize: 8, CacheTTL: time.Minute,
					Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
				})
				ev.Evaluate(deleteStmt)
				return ev, deleteStmt
			},
			want:      policy.FindingCached,
			wantLevel: "medium",
		},
		{
			name: "trigger miss",
			build: func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement) {
				return mustNew(t, analyzer.Config{
					Rule: "risky", Provider: &stubProvider{level: analyzer.RiskHigh},
					Trigger: deleteTrigger(),
					Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
				}), sqlStmt("SELECT 1", hoopinspect.OpSelect)
			},
			want: policy.FindingSkipped,
		},
		{
			name: "provider error",
			build: func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement) {
				return mustNew(t, analyzer.Config{
					Rule: "risky", Provider: &stubProvider{err: errors.New("upstream down")},
					Trigger: deleteTrigger(), FailOpen: true,
					Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
				}), deleteStmt
			},
			want: policy.FindingError,
		},
		{
			name: "budget exhausted",
			build: func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement) {
				ev := mustNew(t, analyzer.Config{
					Rule: "risky", Provider: &stubProvider{level: analyzer.RiskHigh},
					Trigger: deleteTrigger(), MaxCalls: 1,
					Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
				})
				ev.Evaluate(deleteStmt)
				return ev, sqlStmt("DELETE FROM orders", hoopinspect.OpDelete, "orders")
			},
			want: policy.FindingUnavailable,
		},
		{
			name: "refused before sending",
			build: func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement) {
				return mustNew(t, analyzer.Config{
					Rule: "risky", Provider: &stubProvider{level: analyzer.RiskLow},
					Trigger: deleteTrigger(),
					Redact:  func(string) string { return analyzer.RefuseSentinel },
					Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
				}), deleteStmt
			},
			want: policy.FindingUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, stmt := tc.build(t)
			ec := &policy.EvalContext{}
			ev.EvaluateWith(stmt, ec)

			f, ok := ec.Finding(analyzer.Source)
			if !ok {
				t.Fatalf("no finding reported: %+v", ec.Findings)
			}
			if f.Status != tc.want {
				t.Errorf("finding status = %q, want %q", f.Status, tc.want)
			}
			level, _ := f.Values["risk_level"].(string)
			if level != tc.wantLevel {
				t.Errorf("finding risk_level = %q, want %q", level, tc.wantLevel)
			}
		})
	}
}

// A spent budget and a refusal to transmit are unavailable to a policy and
// keep their own word in the trail. The two channels answer different
// questions: Rego wants one status it can branch on without learning this
// package's vocabulary, and an operator tuning max_calls is chasing something
// else than one tuning send: refuse. Getting them backwards leaves Rego
// matching on a word it never heard of and the trail unable to tell the two
// outages apart, so both sides are pinned here.
func TestUnavailableSplitsTheTrailFromTheFinding(t *testing.T) {
	deleteStmt := sqlStmt("DELETE FROM customers", hoopinspect.OpDelete, "customers")

	for _, tc := range []struct {
		name  string
		build func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement)
		word  string
	}{
		{
			name: "budget exhausted",
			build: func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement) {
				ev := mustNew(t, analyzer.Config{
					Rule: "risky", Provider: &stubProvider{level: analyzer.RiskHigh},
					Trigger: deleteTrigger(), MaxCalls: 1,
					Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
				})
				ev.Evaluate(deleteStmt)
				return ev, sqlStmt("DELETE FROM orders", hoopinspect.OpDelete, "orders")
			},
			word: analyzer.StatusBudget,
		},
		{
			name: "refused before sending",
			build: func(t *testing.T) (*analyzer.Evaluator, hoopinspect.Statement) {
				return mustNew(t, analyzer.Config{
					Rule: "risky", Provider: &stubProvider{level: analyzer.RiskLow},
					Trigger: deleteTrigger(),
					Redact:  func(string) string { return analyzer.RefuseSentinel },
					Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
				}), deleteStmt
			},
			word: analyzer.StatusRefused,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, stmt := tc.build(t)
			ec := &policy.EvalContext{}
			v := ev.EvaluateWith(stmt, ec)

			// The trail keeps the specific word.
			if got := v.Annotations[analyzer.MetadataAIStatus]; got != tc.word {
				t.Errorf("ai_status = %q, want %q", got, tc.word)
			}

			// The finding generalizes it and names the cause in reason.
			f, ok := ec.Finding(analyzer.Source)
			if !ok {
				t.Fatalf("no finding reported: %+v", ec.Findings)
			}
			if f.Status != policy.FindingUnavailable {
				t.Errorf("finding status = %q, want %q", f.Status, policy.FindingUnavailable)
			}
			if f.Reason != tc.word {
				t.Errorf("finding reason = %q, want %q", f.Reason, tc.word)
			}
		})
	}
}

// Two ai_analysis rules on one lane share one finding, and the analyzer folds
// them itself. A rule that could not answer outranks one that did, in either
// order: a second rule succeeding must not hide the first one's outage, or a
// policy gating on status == "ok" enforces on half a picture.
func TestFindingFoldKeepsTheMostDegradedStatus(t *testing.T) {
	broken := func(t *testing.T) *analyzer.Evaluator {
		return mustNew(t, analyzer.Config{
			Rule: "outage", Provider: &stubProvider{err: errors.New("upstream down")},
			Trigger: deleteTrigger(), FailOpen: true,
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
		})
	}
	working := func(t *testing.T) *analyzer.Evaluator {
		return mustNew(t, analyzer.Config{
			Rule: "healthy", Provider: &stubProvider{level: analyzer.RiskHigh},
			Trigger: deleteTrigger(),
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
		})
	}

	for _, tc := range []struct {
		name          string
		first, second func(t *testing.T) *analyzer.Evaluator
	}{
		{"outage first", broken, working},
		{"outage second", working, broken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stmt := sqlStmt("DELETE FROM customers", hoopinspect.OpDelete, "customers")
			ec := &policy.EvalContext{}
			tc.first(t).EvaluateWith(stmt, ec)
			tc.second(t).EvaluateWith(stmt, ec)

			f, ok := ec.Finding(analyzer.Source)
			if !ok {
				t.Fatalf("no finding reported: %+v", ec.Findings)
			}
			if f.Status != policy.FindingError {
				t.Errorf("finding status = %q, want %q", f.Status, policy.FindingError)
			}
		})
	}
}

// Among two rules that both answered, the higher level wins in either order.
// A rule scoped narrowly enough to rate a statement low must not erase one
// that rated it high, which is the same max-wins rule the session rollup uses.
func TestFindingFoldKeepsTheHighestLevel(t *testing.T) {
	rule := func(name string, level analyzer.RiskLevel) func(t *testing.T) *analyzer.Evaluator {
		return func(t *testing.T) *analyzer.Evaluator {
			return mustNew(t, analyzer.Config{
				Rule: name, Provider: &stubProvider{level: level},
				Trigger: deleteTrigger(),
				Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
			})
		}
	}
	low := rule("narrow", analyzer.RiskLow)
	high := rule("broad", analyzer.RiskHigh)

	for _, tc := range []struct {
		name          string
		first, second func(t *testing.T) *analyzer.Evaluator
	}{
		{"high first", high, low},
		{"high second", low, high},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stmt := sqlStmt("DELETE FROM customers", hoopinspect.OpDelete, "customers")
			ec := &policy.EvalContext{}
			tc.first(t).EvaluateWith(stmt, ec)
			tc.second(t).EvaluateWith(stmt, ec)

			f, ok := ec.Finding(analyzer.Source)
			if !ok {
				t.Fatalf("no finding reported: %+v", ec.Findings)
			}
			if f.Status != policy.FindingOK {
				t.Errorf("finding status = %q, want %q", f.Status, policy.FindingOK)
			}
			if got, _ := f.Values["risk_level"].(string); got != "high" {
				t.Errorf("finding risk_level = %q, want high", got)
			}
		})
	}
}

// A response frame is not something the analyzer can act on, so it must
// report on neither channel: no ai_status, because "no analyzer here" and
// "analyzer declined" are different facts and an audit row conflating them is
// worse than a missing key, and no finding, because a decide-phase policy
// reading one would see a producer that failed on a row no producer could
// ever have run on.
func TestResponseSideReportsNothing(t *testing.T) {
	ev := mustNew(t, analyzer.Config{
		Rule: "risky", Provider: &stubProvider{level: analyzer.RiskHigh},
		Trigger: deleteTrigger(),
		Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
	})

	resp := sqlStmt("DELETE FROM customers", hoopinspect.OpDelete, "customers")
	resp.Direction = hoopinspect.FromServer

	ec := &policy.EvalContext{}
	if v := ev.EvaluateWith(resp, ec); len(v.Annotations) != 0 {
		t.Errorf("a response frame carried annotations: %v", v.Annotations)
	}
	if len(ec.Findings) != 0 {
		t.Errorf("a response frame reported a finding: %v", ec.Findings)
	}
}

// The enum has to accept defer or a config naming it fails for the wrong
// reason. require_review stays refused; that check lives beside it.
func TestDeferIsAValidAction(t *testing.T) {
	if !analyzer.ActionDefer.Valid() {
		t.Error("defer is not a valid action")
	}
	_, err := analyzer.New(analyzer.Config{
		Provider: &stubProvider{}, Trigger: deleteTrigger(),
		Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
	})
	if err != nil {
		t.Errorf("defer was refused at construction: %v", err)
	}
}

// The fold must not depend on which rule ran first. A degraded outcome
// carries no level either way: Answered() false has to mean "nothing here to
// read", or a policy guarding on it still finds a level and trusts it.
//
// The level is not lost. The risk_level ANNOTATION is a separate channel that
// keeps the highest seen, so the audit record still shows the high.
func TestFindingFoldIsOrderIndependent(t *testing.T) {
	build := func(rule string, p analyzer.Provider) *analyzer.Evaluator {
		return mustNew(t, analyzer.Config{
			Rule: rule, Provider: p, Trigger: deleteTrigger(), FailOpen: true,
			Actions: analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionDefer},
		})
	}
	stmt := sqlStmt("DELETE FROM customers", hoopinspect.OpDelete, "customers")

	for _, tc := range []struct {
		name     string
		errFirst bool
	}{
		{"error then success", true},
		{"success then error", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := build("broken", &stubProvider{err: errors.New("provider down")})
			good := build("healthy", &stubProvider{level: analyzer.RiskHigh})

			order := []*analyzer.Evaluator{bad, good}
			if !tc.errFirst {
				order = []*analyzer.Evaluator{good, bad}
			}
			ec := &policy.EvalContext{}
			for _, ev := range order {
				ev.EvaluateWith(stmt, ec)
			}

			f, ok := ec.Finding(analyzer.Source)
			if !ok {
				t.Fatal("no finding recorded")
			}
			if f.Status != policy.FindingError {
				t.Errorf("Status = %q, want the outage to survive the success", f.Status)
			}
			if f.Answered() {
				t.Error("an errored finding reported itself as answered")
			}
			if f.Values != nil {
				t.Errorf("Values = %v on an unanswered finding; a policy guarding on "+
					"Answered() would still find a level here", f.Values)
			}
		})
	}
}
