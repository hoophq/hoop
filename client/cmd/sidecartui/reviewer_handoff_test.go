package sidecartui

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// handoffStub rates every statement medium and answers each hand-off with
// the next tool in replies, the last one repeating.
type handoffStub struct {
	mu      sync.Mutex
	replies []string
	asked   int
}

func (*handoffStub) Name() string { return "stub" }

func (*handoffStub) Classify(context.Context, string, string) (*analyzer.Result, error) {
	return &analyzer.Result{RiskLevel: analyzer.RiskMedium, Title: "dangerous statement"}, nil
}

func (h *handoffStub) Decide(context.Context, string, string, []analyzer.ToolSpec) ([]analyzer.ToolCall, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tool := h.replies[min(h.asked, len(h.replies)-1)]
	h.asked++
	return []analyzer.ToolCall{{Name: tool, Args: json.RawMessage(`{"reason":"checked"}`)}}, nil
}

func (h *handoffStub) asks() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.asked
}

// A claim on a review the terminal no longer keeps reads as not found, so a
// hand-off pin on a pruned review is dropped and the agent decides again,
// rather than every resend denying until the pin expires.
func TestAPrunedReviewDropsAHandoffPin(t *testing.T) {
	r := NewReviewer()
	if _, err := r.For("pg").Claim(context.Background(), "local-nope"); !errors.Is(err, analyzer.ErrReviewNotFound) {
		t.Errorf("a claim on an unknown review = %v, want ErrReviewNotFound", err)
	}

	agent := &handoffStub{replies: []string{"require_approval", "allow"}}
	ev, err := analyzer.New(analyzer.Config{
		Rule: "pg", Listener: "pg", Provider: agent, Timeout: time.Second,
		Trigger:    analyzer.Trigger{All: true},
		Actions:    analyzer.ActionMap{analyzer.RiskMedium: analyzer.ActionHandoff},
		Handoffs:   map[analyzer.RiskLevel]analyzer.Handoff{analyzer.RiskMedium: {Approval: analyzer.Approval{Approver: analyzer.ApproverHuman}}},
		Review:     r.For("pg"),
		ReviewMode: analyzer.ReviewReturn,
	})
	if err != nil {
		t.Fatal(err)
	}
	stmt := inspect.Statement{Protocol: inspect.Postgres, Direction: inspect.FromClient,
		Text: "DELETE FROM users", Operation: inspect.OpDelete, Tables: []string{"users"}}

	v := ev.Evaluate(stmt)
	id := v.Annotations[analyzer.MetadataReviewID]
	if !v.Denied || id == "" {
		t.Fatalf("first: %+v, want a pending local review", v)
	}
	// Prune drops the review, as it does past maxSettled.
	r.mu.Lock()
	delete(r.reviews, id)
	r.mu.Unlock()

	v = ev.Evaluate(stmt)
	if v.Denied || v.Annotations[analyzer.MetadataHandoffDecision] != "allow" || agent.asks() != 2 {
		t.Fatalf("resend after pruning: %+v after %d ask(s), want the agent's new allow", v, agent.asks())
	}
}
