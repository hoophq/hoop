package services

import (
	"errors"
	"testing"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/gateway/models"
)

// The bar is the half of the policy nothing downstream restates.
// doIndividualReview falls back to the number of group rows when the review
// carries no usable minimum, so each of these cases decides how many people
// have to act before a statement runs.
func TestReviewPolicyFromRuleMinApprovals(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		reviewersGroups      []string
		minApprovals         *int
		allGroupsMustApprove bool
		want                 int
	}{
		{
			name:            "a minimum lowers the bar below the group count",
			reviewersGroups: []string{"dba", "sre"},
			minApprovals:    ptr.Int(1),
			want:            1,
		},
		{
			name:            "no minimum means every group approves",
			reviewersGroups: []string{"dba", "sre"},
			want:            2,
		},
		{
			name:                 "all_groups_must_approve wins over the minimum",
			reviewersGroups:      []string{"dba", "sre"},
			minApprovals:         ptr.Int(1),
			allGroupsMustApprove: true,
			want:                 2,
		},
		{
			name:            "one group, one approval",
			reviewersGroups: []string{"dba"},
			want:            1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := &models.AccessRequestRule{
				Name:                 "payments-approvers",
				ReviewersGroups:      tc.reviewersGroups,
				MinApprovals:         tc.minApprovals,
				AllGroupsMustApprove: tc.allGroupsMustApprove,
			}

			policy, err := ReviewPolicyFromRule("org-1", rule)
			if err != nil {
				t.Fatalf("ReviewPolicyFromRule: %v", err)
			}
			if policy.MinApprovals != tc.want {
				t.Errorf("MinApprovals = %d; want %d", policy.MinApprovals, tc.want)
			}
			if len(policy.Groups) != len(tc.reviewersGroups) {
				t.Fatalf("got %d group rows; want one per reviewers_groups entry (%d)",
					len(policy.Groups), len(tc.reviewersGroups))
			}
		})
	}
}

// CreateReview inserts these rows verbatim, so a row missing its own id or its
// organization is a review a reviewer cannot be matched against.
func TestReviewPolicyFromRuleBuildsOneRowPerGroup(t *testing.T) {
	rule := &models.AccessRequestRule{
		Name:            "payments-approvers",
		ReviewersGroups: []string{"dba", "sre"},
	}

	policy, err := ReviewPolicyFromRule("org-1", rule)
	if err != nil {
		t.Fatalf("ReviewPolicyFromRule: %v", err)
	}

	seen := map[string]bool{}
	for _, rg := range policy.Groups {
		if rg.ID == "" {
			t.Errorf("group %q has no id", rg.GroupName)
		}
		if rg.OrgID != "org-1" {
			t.Errorf("group %q org = %q; want org-1", rg.GroupName, rg.OrgID)
		}
		if rg.Status != models.ReviewStatusPending {
			t.Errorf("group %q status = %q; want pending", rg.GroupName, rg.Status)
		}
		if seen[rg.ID] {
			t.Errorf("group %q reuses an id", rg.GroupName)
		}
		seen[rg.ID] = true
	}
	if got := []string{policy.Groups[0].GroupName, policy.Groups[1].GroupName}; got[0] != "dba" || got[1] != "sre" {
		t.Errorf("group names = %v; want the rule's reviewers_groups in order", got)
	}
}

// A review built from a rule that names nobody can never be approved. The
// sentinel is what lets the sidecar handler answer the operator with a 422
// instead of reporting a server fault.
func TestReviewPolicyFromRuleRefusesARuleWithNoReviewers(t *testing.T) {
	rule := &models.AccessRequestRule{Name: "payments-approvers"}

	_, err := ReviewPolicyFromRule("org-1", rule)

	if !errors.Is(err, ErrRuleHasNoReviewers) {
		t.Fatalf("err = %v; want ErrRuleHasNoReviewers", err)
	}
	// The AI review path wraps this text with its own prefix, so the wording
	// is what a gateway operator reads. It names the rule and nothing else:
	// a sidecar or a listener has no meaning on that path.
	if got := err.Error(); got != `access request rule "payments-approvers" has no reviewers_groups configured` {
		t.Errorf("message = %q; want the rule named and nothing more", got)
	}
}

func TestReviewPolicyFromRuleRefusesANilRule(t *testing.T) {
	if _, err := ReviewPolicyFromRule("org-1", nil); err == nil {
		t.Fatal("a nil rule built a policy")
	}
}
