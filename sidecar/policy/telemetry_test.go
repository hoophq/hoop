package policy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/telemetry"
)

// stageLog records every stage the chain opened and how it ended, and
// stamps the context it handed out so an evaluator can prove it ran under
// the stage's context.
type stageLog struct {
	opened []string
	ended  []string
	// current is the stage whose context is live; "" between stages.
	current string
}

type stageKey struct{}

func (l *stageLog) BeginStage(ctx context.Context, stage telemetry.Stage, engine string) (context.Context, telemetry.EndStage) {
	name := string(stage)
	if engine != "" {
		name += "/" + engine
	}
	l.opened = append(l.opened, name)
	l.current = name
	return context.WithValue(ctx, stageKey{}, name), func(outcome string, err error) {
		l.current = ""
		if err != nil {
			outcome += "+err"
		}
		l.ended = append(l.ended, name+"="+outcome)
	}
}

// stagedFake is an evaluator that names its stage and reports which stage
// context it saw.
type stagedFake struct {
	stage   telemetry.Stage
	engine  string
	verdict policy.Verdict
	sawCtx  string
}

func (f *stagedFake) TelemetryStage() (telemetry.Stage, string) { return f.stage, f.engine }
func (f *stagedFake) Evaluate(inspect.Statement) policy.Verdict { return f.verdict }
func (f *stagedFake) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	if ec.ConnCtx != nil {
		f.sawCtx, _ = ec.ConnCtx.Value(stageKey{}).(string)
	}
	return f.verdict
}

// unstagedFake is a plain evaluator; the chain must not time it.
type unstagedFake struct{}

func (unstagedFake) Evaluate(inspect.Statement) policy.Verdict { return policy.Allow() }

// The chain times each evaluator that names a stage, under a context that
// carries the stage, and restores the connection context after each one so
// a later evaluator does not run under an earlier stage.
func TestChainTimesEachStagedEvaluator(t *testing.T) {
	log := &stageLog{}
	rules := &stagedFake{stage: telemetry.StagePolicy, engine: telemetry.EngineRules, verdict: policy.Allow()}
	ai := &stagedFake{stage: telemetry.StageAnalyzer, verdict: policy.Verdict{Err: errors.New("provider slow")}}
	opa := &stagedFake{stage: telemetry.StagePolicy, engine: telemetry.EngineOPA, verdict: policy.Deny("r", "no")}

	root := context.WithValue(context.Background(), stageKey{}, "connection")
	ec := &policy.EvalContext{ConnCtx: root, Telemetry: log}
	v := policy.Chain{rules, unstagedFake{}, ai, opa}.EvaluateWith(inspect.Statement{}, ec)

	if !v.Denied {
		t.Fatalf("the OPA denial was lost: %+v", v)
	}
	wantOpened := []string{"policy/rules", "analyzer", "policy/opa"}
	if got := log.opened; len(got) != 3 || got[0] != wantOpened[0] || got[1] != wantOpened[1] || got[2] != wantOpened[2] {
		t.Errorf("stages opened = %v, want %v", got, wantOpened)
	}
	wantEnded := []string{"policy/rules=allow", "analyzer=allow+err", "policy/opa=deny"}
	if got := log.ended; len(got) != 3 || got[0] != wantEnded[0] || got[1] != wantEnded[1] || got[2] != wantEnded[2] {
		t.Errorf("stages ended = %v, want %v", got, wantEnded)
	}
	for _, f := range []*stagedFake{rules, ai, opa} {
		want, _, _ := func() (string, telemetry.Stage, string) {
			s, e := f.TelemetryStage()
			n := string(s)
			if e != "" {
				n += "/" + e
			}
			return n, s, e
		}()
		if f.sawCtx != want {
			t.Errorf("%s ran under context %q, want its own stage", want, f.sawCtx)
		}
	}
	if got, _ := ec.ConnCtx.Value(stageKey{}).(string); got != "connection" {
		t.Errorf("ConnCtx after the chain carries %q; the connection context was not restored", got)
	}
}

// Without a recorder the chain opens nothing and the evaluators see the
// connection context untouched: the off path has no telemetry seam in it.
func TestChainWithoutRecorderTouchesNothing(t *testing.T) {
	rules := &stagedFake{stage: telemetry.StagePolicy, engine: telemetry.EngineRules, verdict: policy.Allow()}
	root := context.WithValue(context.Background(), stageKey{}, "connection")
	policy.Chain{rules}.EvaluateWith(inspect.Statement{}, &policy.EvalContext{ConnCtx: root})
	if rules.sawCtx != "connection" {
		t.Errorf("evaluator ran under %q; without telemetry it must see the connection context", rules.sawCtx)
	}
}

// requester asks for a review that resolves to the verdict it was given,
// so the test controls how the hold ends.
type reviewFake struct{ resolved policy.Verdict }

func (r reviewFake) Evaluate(inspect.Statement) policy.Verdict { return policy.Allow() }
func (r reviewFake) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	ec.RequestReview(policy.ReviewRequest{Resolve: func() policy.Verdict { return r.resolved }})
	return policy.Allow()
}

// The review hold is its own stage, and its outcome is the review's word:
// an approver's rejection, an approval, and a client that left are three
// different facts even where two of them are the same verdict.
func TestReviewHoldIsTimedWithItsOwnOutcome(t *testing.T) {
	cases := map[string]struct {
		resolved policy.Verdict
		connGone bool
		want     string
	}{
		"approved": {resolved: policy.Allow(), want: "review=approved"},
		"rejected": {
			resolved: policy.Verdict{Denied: true, Source: policy.SourceReview, Review: &policy.Review{Status: "REJECTED"}},
			want:     "review=rejected",
		},
		"expired": {
			resolved: policy.Verdict{Denied: true, Source: policy.SourceReview, Review: &policy.Review{Status: "EXPIRED"}},
			want:     "review=expired",
		},
		"client left": {
			resolved: policy.Verdict{Denied: true, Source: policy.SourceReview, Review: &policy.Review{Status: "APPROVED"}},
			connGone: true,
			want:     "review=disconnected",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			log := &stageLog{}
			ctx, cancel := context.WithCancel(context.Background())
			if tc.connGone {
				cancel()
			} else {
				defer cancel()
			}
			ec := &policy.EvalContext{ConnCtx: ctx, Telemetry: log}
			policy.Chain{reviewFake{resolved: tc.resolved}}.EvaluateWith(inspect.Statement{}, ec)
			if len(log.ended) != 1 || log.ended[0] != tc.want {
				t.Errorf("stages ended = %v, want [%s]", log.ended, tc.want)
			}
		})
	}
}
