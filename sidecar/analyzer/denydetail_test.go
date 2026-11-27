package analyzer_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

type explainingProvider struct {
	level       analyzer.RiskLevel
	title       string
	explanation string
}

func (explainingProvider) Name() string { return "stub" }

func (p explainingProvider) Classify(context.Context, string, string) (*analyzer.Result, error) {
	return &analyzer.Result{RiskLevel: p.level, Title: p.title, Explanation: p.explanation}, nil
}

func blockEvaluator(t *testing.T, p explainingProvider, message string) *analyzer.Evaluator {
	t.Helper()
	return mustNew(t, analyzer.Config{
		Rule:     "risky",
		Provider: p,
		Trigger:  deleteTrigger(),
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock, analyzer.RiskLow: analyzer.ActionWarn},
		Message:  message,
	})
}

func TestABlockSendsTheExplanationAsDetail(t *testing.T) {
	ev := blockEvaluator(t, explainingProvider{
		level:       analyzer.RiskHigh,
		title:       "unbounded delete",
		explanation: "The DELETE has no WHERE clause, so it removes every row.",
	}, "")
	v := ev.Evaluate(sqlStmt("DELETE FROM customers", inspect.OpDelete, "customers"))
	if !v.Denied {
		t.Fatal("not denied")
	}
	if v.Message != "unbounded delete" {
		t.Errorf("Message = %q, want the title alone", v.Message)
	}
	if v.Detail != "The DELETE has no WHERE clause, so it removes every row." {
		t.Errorf("Detail = %q, want the explanation", v.Detail)
	}
	for k, val := range v.Annotations {
		if strings.Contains(val, "WHERE clause") {
			t.Errorf("annotation %s carries the explanation: %q", k, val)
		}
	}
}

func TestALaneMessageReplacesTheExplanation(t *testing.T) {
	ev := blockEvaluator(t, explainingProvider{
		level: analyzer.RiskHigh, title: "unbounded delete", explanation: "no WHERE clause",
	}, "refused by risk analysis")
	v := ev.Evaluate(sqlStmt("DELETE FROM customers", inspect.OpDelete, "customers"))
	if v.Message != "refused by risk analysis" || v.Detail != "" {
		t.Errorf("Message, Detail = %q, %q; want the lane message and no detail", v.Message, v.Detail)
	}
}

func TestADetailIsOneLineWithNoControlCharacters(t *testing.T) {
	ev := blockEvaluator(t, explainingProvider{
		level: analyzer.RiskHigh, title: "unbounded delete",
		explanation: "line one\r\nline\ttwo\x00 three\x1b[31m  four\n",
	}, "")
	v := ev.Evaluate(sqlStmt("DELETE FROM customers", inspect.OpDelete, "customers"))
	if v.Detail != "line one line two three [31m four" {
		t.Errorf("Detail = %q", v.Detail)
	}
}

func TestALongDetailIsCutOnARuneBoundary(t *testing.T) {
	ev := blockEvaluator(t, explainingProvider{
		level: analyzer.RiskHigh, title: "unbounded delete", explanation: strings.Repeat("é", 2000),
	}, "")
	v := ev.Evaluate(sqlStmt("DELETE FROM customers", inspect.OpDelete, "customers"))
	if len(v.Detail) > 1024 || !utf8.ValidString(v.Detail) || !strings.HasSuffix(v.Detail, "...") {
		t.Errorf("Detail is %d bytes, valid UTF-8 %v: %q", len(v.Detail), utf8.ValidString(v.Detail), v.Detail)
	}
	for _, r := range v.Detail {
		if unicode.IsControl(r) {
			t.Fatalf("Detail carries a control character: %q", v.Detail)
		}
	}
}

func TestOnlyABlockSendsADetail(t *testing.T) {
	ev := blockEvaluator(t, explainingProvider{
		level: analyzer.RiskLow, title: "a read", explanation: "bounded",
	}, "")
	if v := ev.Evaluate(sqlStmt("DELETE FROM customers WHERE id = 1", inspect.OpDelete, "customers")); v.Denied || v.Detail != "" {
		t.Errorf("warn: Denied %v, Detail %q", v.Denied, v.Detail)
	}

	failing := mustNew(t, analyzer.Config{
		Provider: &stubProvider{err: errors.New("provider down")},
		Trigger:  deleteTrigger(),
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
	})
	if v := failing.Evaluate(sqlStmt("DELETE FROM customers", inspect.OpDelete, "customers")); !v.Denied || v.Detail != "" {
		t.Errorf("provider failure: Denied %v, Detail %q", v.Denied, v.Detail)
	}
}

func TestAHandoffBlockSendsTheExplanationAsDetail(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply decideReply
	}{
		{"agent blocks", pick("block", "deletes too much")},
		{"agent fails", decideReply{err: errors.New("model down")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &handoffProvider{replies: []decideReply{tc.reply}}
			ev := handoffEvaluator(t, p, analyzer.Handoff{Allowed: []analyzer.Action{analyzer.ActionBlock}}, nil)
			v := ev.Evaluate(sqlStmt("DELETE FROM customers", inspect.OpDelete, "customers"))
			if !v.Denied {
				t.Fatal("not denied")
			}
			if v.Detail != "because reasons" {
				t.Errorf("Detail = %q, want the classification's explanation", v.Detail)
			}
			if strings.Contains(v.Message, "because reasons") {
				t.Errorf("Message carries the explanation: %q", v.Message)
			}
		})
	}
}
