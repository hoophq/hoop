package gate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/hoophq/hoop/sidecar/telemetry"
)

// laneLog is a LaneRecorder that records what the gate reported, and
// stamps the statement context so the test can see it reach the audit
// write and the policy chain.
type laneLog struct {
	statements []telemetry.Verdict
	stages     []string
	masked     int
	auditErrs  int
	// stmtCtxSeen is set by the audit sink when the write arrived under
	// the statement's context.
	stmtCtxSeen bool
}

type stmtKey struct{}

func (l *laneLog) BeginExchange(ctx context.Context, _ telemetry.ExchangeKind, _ ...telemetry.Attr) (context.Context, telemetry.EndExchange) {
	return ctx, func(error) {}
}

func (l *laneLog) BeginStatement(ctx context.Context, _ inspect.Statement) (context.Context, telemetry.EndStatement) {
	return context.WithValue(ctx, stmtKey{}, true), func(v telemetry.Verdict) {
		l.statements = append(l.statements, v)
	}
}

func (l *laneLog) BeginStage(ctx context.Context, stage telemetry.Stage, engine string) (context.Context, telemetry.EndStage) {
	if ctx.Value(stmtKey{}) == nil {
		panic("stage opened outside the statement's context")
	}
	return ctx, func(outcome string, _ error) {
		l.stages = append(l.stages, string(stage)+"/"+engine+"="+outcome)
	}
}

func (l *laneLog) Upstream(context.Context, time.Duration, telemetry.Pairing) {}
func (l *laneLog) Masked(values int)                                          { l.masked += values }
func (l *laneLog) AuditError()                                                { l.auditErrs++ }

// ctxSink records whether the write came under the statement context, and
// fails when told to.
type ctxSink struct {
	log  *laneLog
	fail bool
}

func (s *ctxSink) Write(ctx context.Context, _ audit.Event) error {
	if ctx.Value(stmtKey{}) != nil {
		s.log.stmtCtxSeen = true
	}
	if s.fail {
		return errors.New("disk full")
	}
	return nil
}
func (s *ctxSink) Close() error { return nil }

// One judged statement reaches the recorder once, with what the client saw:
// the verdict, the denying source and rule, and the stages the chain ran
// under the statement's own context, which the audit write also carries so
// a log record can join the span.
func TestGateReportsEachStatementWithItsStagesAndVerdict(t *testing.T) {
	log := &laneLog{}
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "no-drops", Type: policy.MatchDenyWords, Words: []string{"DROP"},
		Message: "no",
	}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := gate.NewStatementGate(
		session.New(inspect.GRPC, session.Identity{Subject: "alice"}),
		gate.Config{
			Protocol:  inspect.GRPC,
			Policy:    policy.Chain{rules},
			Audit:     &ctxSink{log: log},
			Telemetry: log,
		})
	if err != nil {
		t.Fatalf("NewStatementGate: %v", err)
	}
	ctx := context.Background()
	if d := g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "SELECT 1")); !d.Allowed {
		t.Fatalf("SELECT denied: %+v", d)
	}
	if d := g.EvaluateStatement(ctx, grpcStmt(inspect.FromClient, "DROP TABLE t")); d.Allowed {
		t.Fatal("DROP allowed")
	}

	if len(log.statements) != 2 {
		t.Fatalf("statements reported = %d, want 2", len(log.statements))
	}
	if v := log.statements[0]; v.Denied || v.Source != "" {
		t.Errorf("allowed statement reported as %+v", v)
	}
	if v := log.statements[1]; !v.Denied || v.Rule != "no-drops" || v.Source == "" {
		t.Errorf("denied statement reported as %+v; want denied by no-drops with a source", v)
	}
	wantStages := []string{"policy/rules=allow", "policy/rules=deny"}
	if len(log.stages) != 2 || log.stages[0] != wantStages[0] || log.stages[1] != wantStages[1] {
		t.Errorf("stages = %v, want %v", log.stages, wantStages)
	}
	if !log.stmtCtxSeen {
		t.Error("the audit write did not run under the statement's context; a log record could not join its span")
	}
}

// A fail-closed audit refusal is what the client saw, so the recorder is
// told a denial from the audit source, and the write failure is counted.
func TestGateReportsAuditRefusalAsADenial(t *testing.T) {
	log := &laneLog{}
	g, err := gate.NewStatementGate(
		session.New(inspect.GRPC, session.Identity{Subject: "alice"}),
		gate.Config{
			Protocol:         inspect.GRPC,
			Audit:            &ctxSink{log: log, fail: true},
			FailOnAuditError: true,
			Telemetry:        log,
		})
	if err != nil {
		t.Fatalf("NewStatementGate: %v", err)
	}
	if d := g.EvaluateStatement(context.Background(), grpcStmt(inspect.FromClient, "SELECT 1")); d.Allowed {
		t.Fatal("statement allowed with the audit sink down")
	}
	if len(log.statements) != 1 || !log.statements[0].Denied || log.statements[0].Source != gate.SourceAudit {
		t.Errorf("reported %+v, want a denial from %q", log.statements, gate.SourceAudit)
	}
	if log.auditErrs != 1 {
		t.Errorf("audit errors counted = %d, want 1", log.auditErrs)
	}
}
