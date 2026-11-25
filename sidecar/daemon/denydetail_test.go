package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

const (
	detailTitle  = "unbounded delete"
	detailReason = "The command removes every file under the path."
	detailJoined = detailTitle + ": " + detailReason
)

type detailPolicy struct{}

func (detailPolicy) Evaluate(inspect.Statement) policy.Verdict {
	v := policy.Deny("risky", detailTitle)
	v.Detail = detailReason
	return v
}

func assertTrailHasNoDetail(t *testing.T, events []audit.Event) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("nothing was recorded")
	}
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "every file") {
			t.Errorf("the audit trail carries the detail: %s", b)
		}
	}
}

func TestAnSSHRefusalCarriesTheDetail(t *testing.T) {
	sess := session.New(inspect.SSH, session.Identity{Subject: "alice@example.com"})
	sink := &sshTestSink{}
	g, err := gate.NewStatementGate(sess, gate.Config{Protocol: inspect.SSH, Policy: detailPolicy{}, Audit: sink})
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	c := &sshConnState{gate: g, release: func() {}, stmts: sshStatements{},
		log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if r := c.exec(context.Background(), "rm -rf /srv"); r.String() != detailJoined {
		t.Errorf("exec refusal = %q, want %q", r.String(), detailJoined)
	}
	if r := c.sftpOp(context.Background(), inspect.OpSFTPRemove, "/srv/data.csv", ""); r.String() != detailJoined {
		t.Errorf("sftp refusal = %q, want %q", r.String(), detailJoined)
	}
	assertTrailHasNoDetail(t, sink.snapshot())
}

func TestAGRPCDenialStatusCarriesTheDetail(t *testing.T) {
	st := grpcDeniedStatus(gate.Decision{Message: detailTitle, Detail: detailReason})
	if st.Code != grpcPermissionDenied || st.Message != detailJoined {
		t.Errorf("status = %d %q", st.Code, st.Message)
	}
	if st := grpcDeniedStatus(gate.Decision{Message: detailTitle}); st.Message != detailTitle {
		t.Errorf("no detail: message = %q", st.Message)
	}
}

type explainingToolModel struct{}

func (explainingToolModel) Name() string { return "stub" }

func (explainingToolModel) Classify(context.Context, string, string) (*analyzer.Result, error) {
	return &analyzer.Result{RiskLevel: analyzer.RiskHigh, Title: detailTitle, Explanation: detailReason}, nil
}

func TestAnMCPAnalyzerBlockCarriesTheExplanation(t *testing.T) {
	ls, sink := openMCPAnalyzerSession(t, `{"high":"block"}`, "", explainingToolModel{}, nil, nil)
	v := ls.ToolCall(context.Background(), "delete_files", []byte(`{"path":"/srv"}`))
	if v.Allowed || v.Message != detailTitle || v.Detail != detailReason {
		t.Errorf("verdict = %+v", v)
	}
	assertTrailHasNoDetail(t, sink.Events())
}
