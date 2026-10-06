package analyzer_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// detailReviewer records the verdict each filing carried.
type detailReviewer struct {
	got []analyzer.HoldDetail
	ok  []bool
}

func (d *detailReviewer) File(ctx context.Context, _ string) (analyzer.ReviewResult, error) {
	det, ok := analyzer.HoldDetailFrom(ctx)
	d.got, d.ok = append(d.got, det), append(d.ok, ok)
	return analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}, nil
}

func (d *detailReviewer) Claim(_ context.Context, id string) (analyzer.ReviewResult, error) {
	return analyzer.ReviewResult{ID: id, Status: "REJECTED"}, nil
}

// The person deciding sees why the statement was held: the reviewer's
// context carries the verdict. The trail does not: title and explanation are
// model prose and stay out of the annotations.
func TestTheReviewerSeesWhyTheStatementWasHeld(t *testing.T) {
	rev := &detailReviewer{}
	ev := fastWait(mustNew(t, analyzer.Config{
		Rule:     "payments",
		Provider: &stubProvider{level: analyzer.RiskHigh},
		Trigger:  deleteTrigger(),
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireReview},
		Review:   rev,
	}))
	v := ev.Evaluate(deleteStatement())

	if len(rev.got) != 1 || !rev.ok[0] {
		t.Fatalf("the filing carried no verdict: %+v", rev.ok)
	}
	want := analyzer.HoldDetail{Rule: "payments", RiskLevel: analyzer.RiskHigh,
		Title: "dangerous statement", Explanation: "because reasons"}
	if rev.got[0] != want {
		t.Fatalf("verdict = %+v, want %+v", rev.got[0], want)
	}
	for k, val := range v.Annotations {
		if strings.Contains(val, "because reasons") || strings.Contains(val, "dangerous statement") {
			t.Errorf("model prose reached the trail under %q", k)
		}
	}
	if _, ok := analyzer.HoldDetailFrom(context.Background()); ok {
		t.Error("a context with no hold reported a verdict")
	}
}
