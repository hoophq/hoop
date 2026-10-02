package analyzer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// decideOPA answers every decide call with result and records input.review.
func decideOPA(t *testing.T, result string) (*policy.OPAClient, *map[string]any) {
	t.Helper()
	review := new(map[string]any)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input struct {
				Review map[string]any `json:"review"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		*review = body.Input.Review
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(result))
	}))
	t.Cleanup(srv.Close)
	return &policy.OPAClient{URL: srv.URL, Phase: policy.PhaseDecide}, review
}

// The bug ADR-0024 fixes: the hold filed and spent the approval, then the
// decide phase denied. A decide denial must now file nothing.
func TestADecideDenialFilesNoReview(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{
		Forward: true, ID: "9f97", Status: "EXECUTED",
	}}
	opa, review := decideOPA(t, `{"result": {"denied": true, "rule": "breakglass-only"}}`)
	chain := policy.Chain{holdingEvaluator(t, rev, nil), opa}

	v := chain.EvaluateWith(deleteStatement(), &policy.EvalContext{})

	if !v.Denied || v.Rule != "breakglass-only" {
		t.Fatalf("the decide denial did not stand: %+v", v)
	}
	if got := rev.statements(); len(got) != 0 {
		t.Fatalf("filed %q before decide denied", got)
	}
	if (*review)["required"] != true || (*review)["mode"] != "hold" ||
		(*review)["mode_source"] != "listener" {
		t.Fatalf("decide saw input.review = %v", *review)
	}
}

func TestADecideAllowFilesTheReview(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{
		Forward: true, ID: "9f97", Status: "EXECUTED",
	}}
	opa, _ := decideOPA(t, `{"result": {"allow": true}}`)
	chain := policy.Chain{holdingEvaluator(t, rev, nil), opa}

	v := chain.EvaluateWith(deleteStatement(), &policy.EvalContext{})

	if v.Denied {
		t.Fatalf("an approved review after an allow denied: %+v", v)
	}
	if got := rev.statements(); len(got) != 1 {
		t.Fatalf("filed %d reviews, want 1", len(got))
	}
	if v.Annotations[analyzer.MetadataReviewID] != "9f97" ||
		v.Annotations[analyzer.MetadataReviewMode] != "hold" {
		t.Fatalf("the record lost the review: %v", v.Annotations)
	}
}

// Return mode resolves before decide too, so decide sees what the client
// asked for.
func TestDecideSeesTheClientReviewMode(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}}
	opa, review := decideOPA(t, `{"result": {"allow": true}}`)
	chain := policy.Chain{holdingEvaluator(t, rev, postTrigger), opa}

	chain.EvaluateWith(withReviewModeHeader(postStatement(`{"a":1}`, false), "return"),
		&policy.EvalContext{})

	if (*review)["mode"] != "return" || (*review)["mode_source"] != "client" {
		t.Fatalf("decide saw input.review = %v", *review)
	}
}
