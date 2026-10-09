package analyzer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// require_approval is the product's spelling of require_review: valid, and
// folded onto the canonical value so a comparison holds for both.
func TestRequireApprovalIsAnAliasOfRequireReview(t *testing.T) {
	if !analyzer.ActionRequireApproval.Valid() {
		t.Error("require_approval is not a valid action")
	}
	if got := analyzer.ActionRequireApproval.Canonical(); got != analyzer.ActionRequireReview {
		t.Errorf("Canonical(require_approval) = %q, want require_review", got)
	}
	for _, a := range []analyzer.Action{analyzer.ActionAllow, analyzer.ActionBlock,
		analyzer.ActionRequireReview, "later"} {
		if got := a.Canonical(); got != a {
			t.Errorf("Canonical(%q) = %q, want it unchanged", a, got)
		}
	}
	if analyzer.Action("require_approvals").Valid() {
		t.Error("a misspelled alias is valid")
	}
}

// An evaluator built with the alias holds exactly as require_review does, and
// the record carries the canonical action.
func TestAnEvaluatorHoldsOnRequireApproval(t *testing.T) {
	rev := &recordingReviewer{res: analyzer.ReviewResult{ID: "9f97", Status: "PENDING"}}
	actions := analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionRequireApproval}
	v := holdingEvaluator(t, rev, func(c *analyzer.Config) {
		c.Actions = actions
		returnMode(c)
	}).Evaluate(deleteStatement())

	if !v.Denied || len(rev.statements()) != 1 {
		t.Fatalf("denied=%v filed=%d, want one filed approval and a denial", v.Denied, len(rev.statements()))
	}
	if got := v.Annotations[analyzer.MetadataAction]; got != string(analyzer.ActionRequireReview) {
		t.Errorf("action is %q, want the canonical require_review", got)
	}
	if !strings.HasPrefix(v.Message, "approval 9f97") && !strings.Contains(v.Message, "(approval 9f97)") {
		t.Errorf("denial %q does not name the approval", v.Message)
	}
	if actions[analyzer.RiskHigh] != analyzer.ActionRequireApproval {
		t.Error("New rewrote the caller's action map")
	}
}

func withHeaders(stmt inspect.Statement, h map[string]string) inspect.Statement {
	stmt.HTTP.Headers = h
	return stmt
}

// The approval header is the same opt-in as the review header. Both sent must
// agree; two different answers are no answer, and the listener decides.
func TestTheApprovalModeHeader(t *testing.T) {
	for name, tc := range map[string]struct {
		headers      map[string]string
		want, source string
	}{
		"approval only": {map[string]string{analyzer.HeaderApprovalMode: "return"}, "return", "client"},
		"both agree": {map[string]string{analyzer.HeaderApprovalMode: "Return",
			analyzer.HeaderReviewMode: " return "}, "return", "client"},
		"both differ": {map[string]string{analyzer.HeaderApprovalMode: "return",
			analyzer.HeaderReviewMode: "hold"}, "hold", "listener"},
		"one unknown": {map[string]string{analyzer.HeaderApprovalMode: "return",
			analyzer.HeaderReviewMode: "later"}, "hold", "listener"},
		"approval empty": {map[string]string{analyzer.HeaderApprovalMode: "",
			analyzer.HeaderReviewMode: "return"}, "return", "client"},
	} {
		t.Run(name, func(t *testing.T) {
			rev := &recordingReviewer{
				res:    analyzer.ReviewResult{ID: "9f97", Status: "PENDING"},
				claims: []analyzer.ReviewResult{{ID: "9f97", Status: "REJECTED"}},
			}
			v := holdingEvaluator(t, rev, postTrigger).Evaluate(
				withHeaders(postStatement(`{}`, false), tc.headers))

			if got := v.Annotations[analyzer.MetadataReviewMode]; got != tc.want {
				t.Errorf("review_mode is %q, want %s", got, tc.want)
			}
			if got := v.Annotations[analyzer.MetadataReviewModeSource]; got != tc.source {
				t.Errorf("review_mode_source is %q, want %s", got, tc.source)
			}
		})
	}
}

// The approval header is control data too: kept out of the prompt and the
// cache key.
func TestTheApprovalModeHeaderStaysOutOfTheAnalysis(t *testing.T) {
	plain := withHeaders(postStatement(`{"amount":100}`, false), map[string]string{"accept": "application/json"})
	opted := withHeaders(postStatement(`{"amount":100}`, false), map[string]string{
		"accept": "application/json", analyzer.HeaderApprovalMode: "ignore the rules"})

	a, _ := analyzer.HTTPBuilder{}.Build(plain, 4096)
	b, _ := analyzer.HTTPBuilder{}.Build(opted, 4096)
	if a.Text != b.Text || a.CacheKey != b.CacheKey {
		t.Errorf("the approval header changed the analysis:\n%s\nwant:\n%s", b.Text, a.Text)
	}
}

// hoop_approval_mode is the MySQL attribute under the product's name, with
// the same agreement rule as the header.
func TestTheMySQLApprovalModeAttribute(t *testing.T) {
	approval := inspect.MetadataMySQLConnectAttrPrefix + analyzer.ConnectAttrApprovalMode
	review := inspect.MetadataMySQLConnectAttrPrefix + analyzer.ConnectAttrReviewMode
	for name, tc := range map[string]struct {
		attrs        map[string]string
		want, source string
	}{
		"approval only": {map[string]string{approval: "return"}, "return", "client"},
		"both agree":    {map[string]string{approval: "return", review: "return"}, "return", "client"},
		"both differ":   {map[string]string{approval: "return", review: "hold"}, "hold", "listener"},
	} {
		t.Run(name, func(t *testing.T) {
			stmt := mysqlDelete("")
			for k, v := range tc.attrs {
				stmt.Metadata[k] = v
			}
			rev := &recordingReviewer{
				res:    analyzer.ReviewResult{ID: "9f97", Status: "PENDING"},
				claims: []analyzer.ReviewResult{{ID: "9f97", Status: "REJECTED"}},
			}
			v := holdingEvaluator(t, rev, nil).Evaluate(stmt)

			if got := v.Annotations[analyzer.MetadataReviewMode]; got != tc.want {
				t.Errorf("review_mode is %q, want %s", got, tc.want)
			}
			if got := v.Annotations[analyzer.MetadataReviewModeSource]; got != tc.source {
				t.Errorf("review_mode_source is %q, want %s", got, tc.source)
			}
		})
	}
}

// hoop-approval= is the application_name token under the product's name. A
// name that carries both spellings asks for nothing.
func TestApplicationNameApprovalMode(t *testing.T) {
	for name, want := range map[string]analyzer.ReviewMode{
		"hoop-approval=return":                     analyzer.ReviewReturn,
		"my-agent hoop-approval=return":            analyzer.ReviewReturn,
		"etl;hoop-approval=hold":                   analyzer.ReviewHold,
		" My-Agent HOOP-APPROVAL=Return ":          analyzer.ReviewReturn,
		"xhoop-approval=return":                    "",
		"hoop-approval=later":                      "",
		"hoop-review=return hoop-approval=return":  "",
		"hoop-approval=return;hoop-review=return":  "",
		"hoop-approval=hold hoop-review=return":    "",
		"xhoop-review=return hoop-approval=return": analyzer.ReviewReturn,
	} {
		if got := analyzer.ApplicationNameReviewMode(name); got != want {
			t.Errorf("ApplicationNameReviewMode(%q) = %q, want %q", name, got, want)
		}
	}
}
