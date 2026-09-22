package gate_test

import (
	"context"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// countingMetrics records every call the gate makes into gate.Metrics.
type countingMetrics struct {
	mu          sync.Mutex
	statements  []statementCall
	masked      int
	auditErrors int
}

type statementCall struct {
	denied bool
	source string
}

func (m *countingMetrics) Statement(denied bool, source string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statements = append(m.statements, statementCall{denied, source})
}

func (m *countingMetrics) Masked(values int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.masked += values
}

func (m *countingMetrics) AuditError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditErrors++
}

// A statement the policy allowed but the fail-closed audit refused is a
// denial to the client, sourced to the audit, and the metrics must say so.
// It is also exactly one statement, not zero and not two; and the failed
// write is counted as an audit error.
func TestMetricsCountAuditRefusalAsDenied(t *testing.T) {
	m := &countingMetrics{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol:         inspect.Postgres,
		Audit:            &recordingSink{failOn: audit.KindStatement},
		FailOnAuditError: true,
		Metrics:          m,
	})
	d := g.Request(context.Background(), pgQuery("SELECT 1"))
	if d.Allowed {
		t.Fatal("audit refusal did not deny")
	}
	want := []statementCall{{true, gate.SourceAudit}}
	if len(m.statements) != 1 || m.statements[0] != want[0] {
		t.Fatalf("Statement calls = %v, want %v", m.statements, want)
	}
	if stmts, denied := g.Stats(); stmts != 1 || denied != 1 {
		t.Fatalf("Stats = (%d, %d), want (1, 1)", stmts, denied)
	}
	if m.auditErrors != 1 {
		t.Fatalf("audit errors = %d, want 1", m.auditErrors)
	}
}

// Under a working sink the count follows the verdict, and a local rule's
// denial is sourced to its MatchType, never to its name.
func TestMetricsFollowTheVerdictAndNameTheKind(t *testing.T) {
	m := &countingMetrics{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: inspect.Postgres,
		Policy:   denyDrops(t),
		Audit:    &recordingSink{},
		Metrics:  m,
	})
	ctx := context.Background()
	g.Request(ctx, pgQuery("SELECT 1"))
	g.Request(ctx, pgQuery("DROP TABLE t"))
	want := []statementCall{{false, ""}, {true, string(policy.MatchOperation)}}
	if len(m.statements) != 2 || m.statements[0] != want[0] || m.statements[1] != want[1] {
		t.Fatalf("Statement calls = %v, want %v", m.statements, want)
	}
	if m.auditErrors != 0 {
		t.Fatalf("audit errors = %d on a working sink", m.auditErrors)
	}
}

// Under the default (fail open) an audit failure allows the statement and
// is still counted as a write failure.
func TestMetricsCountAuditErrorsUnderFailOpen(t *testing.T) {
	m := &countingMetrics{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: inspect.Postgres,
		Audit:    &recordingSink{failOn: audit.KindStatement},
		Metrics:  m,
	})
	d := g.Request(context.Background(), pgQuery("SELECT 1"))
	if !d.Allowed {
		t.Fatal("fail-open denied")
	}
	if len(m.statements) != 1 || m.statements[0] != (statementCall{false, ""}) {
		t.Fatalf("Statement calls = %v", m.statements)
	}
	if m.auditErrors != 1 {
		t.Fatalf("audit errors = %d, want 1", m.auditErrors)
	}
}

// Rows a reframing codec holds until the result set ends are masked in
// FlushResponse, not in Response. A connection that closes mid-result-set
// masks only there, and those values must still be counted.
func TestMetricsCountValuesMaskedOnFlush(t *testing.T) {
	m := &countingMetrics{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: inspect.Postgres,
		Audit:    &recordingSink{},
		Masker:   stubMasker{find: "ada@example.com", replace: "[REDACTED:email]"},
		Metrics:  m,
	})

	// RowDescription and a DataRow, but no CommandComplete: the codec holds
	// the row, so Response masks nothing yet.
	desc := []byte("T\x00\x00\x00\x1b\x00\x01mail\x00" +
		"\x00\x00\x00\x00\x00\x00\x00\x00\x00\x19\x00\x00\x00\x00\x00\x00\x00\x00")
	row := []byte("D\x00\x00\x00\x19\x00\x01\x00\x00\x00\x0fada@example.com")
	ctx := context.Background()
	g.Response(ctx, desc)
	g.Response(ctx, row)
	if m.masked != 0 {
		t.Fatalf("masked before flush = %d, want 0: the row is still held", m.masked)
	}

	out := g.FlushResponse()
	if len(out) == 0 {
		t.Fatal("flush returned nothing; the held row was lost")
	}
	if m.masked != 1 {
		t.Fatalf("masked after flush = %d, want 1", m.masked)
	}
}

// Session start and end are audit writes too. A sink that fails them must
// show up in the counter exactly once each, and the errors still return.
func TestMetricsCountLifecycleAuditErrors(t *testing.T) {
	for _, kind := range []audit.Kind{audit.KindSessionStart, audit.KindSessionEnd} {
		m := &countingMetrics{}
		g, _ := gate.New(newSession(), gate.Config{
			Protocol: inspect.Postgres,
			Audit:    &recordingSink{failOn: kind},
			Metrics:  m,
		})
		ctx := context.Background()
		startErr := g.Start(ctx)
		closeErr := g.Close(ctx)
		if (startErr != nil) != (kind == audit.KindSessionStart) || (closeErr != nil) != (kind == audit.KindSessionEnd) {
			t.Fatalf("%s: start err=%v close err=%v", kind, startErr, closeErr)
		}
		if m.auditErrors != 1 {
			t.Fatalf("%s: audit errors = %d, want 1", kind, m.auditErrors)
		}
		// Idempotent calls write nothing, so they count nothing.
		_ = g.Start(ctx)
		_ = g.Close(ctx)
		if m.auditErrors != 1 {
			t.Fatalf("%s: repeated Start/Close counted again: %d", kind, m.auditErrors)
		}
	}
}
