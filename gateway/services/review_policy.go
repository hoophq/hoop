package services

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// ErrRuleHasNoReviewers reports a rule that names nobody. It is a sentinel so
// a caller can answer the operator instead of reporting a server fault: a
// review built from such a rule has no group row, and no approval can ever
// settle it.
var ErrRuleHasNoReviewers = errors.New("has no reviewers_groups configured")

// ReviewPolicy is who may approve a review and how many of them must. It is
// the whole approval policy: nothing further down the review path restates it,
// so an omission here is silent.
type ReviewPolicy struct {
	// Groups is one pending row per reviewers_groups entry. Each carries its
	// own id because CreateReview inserts them verbatim.
	Groups []models.ReviewGroups

	// MinApprovals is the bar. Read by doIndividualReview, which without it
	// falls back to the number of group rows and quietly requires every one.
	MinApprovals int
}

// ReviewPolicyFromRule turns an access request rule into the policy a review
// carries. Shared by every path that files a review against a rule, so the two
// of them cannot drift into needing a different number of approvals for the
// same rule.
//
// It names only the rule. A caller that knows more about why it is building a
// policy says so in its own message: this text reaches the gateway's AI review
// path as well.
func ReviewPolicyFromRule(orgID string, rule *models.AccessRequestRule) (*ReviewPolicy, error) {
	if rule == nil {
		return nil, errors.New("access request rule is required")
	}
	if len(rule.ReviewersGroups) == 0 {
		return nil, fmt.Errorf("access request rule %q %w", rule.Name, ErrRuleHasNoReviewers)
	}

	groups := make([]models.ReviewGroups, 0, len(rule.ReviewersGroups))
	for _, groupName := range rule.ReviewersGroups {
		groups = append(groups, models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     orgID,
			GroupName: groupName,
			Status:    models.ReviewStatusPending,
		})
	}

	// all_groups_must_approve wins over the minimum, and an absent minimum
	// means every group too. Only a rule that sets one and does not demand
	// them all lowers the bar.
	minApprovals := len(groups)
	if !rule.AllGroupsMustApprove && rule.MinApprovals != nil {
		minApprovals = *rule.MinApprovals
	}

	return &ReviewPolicy{Groups: groups, MinApprovals: minApprovals}, nil
}

// ReviewPolicyForLevel is the policy of a review filed under a human risk
// level: one pending row per group of approvers.<level>, or the admin group
// when it names none, and one approval releases. With Owners set, the
// reserved sidecar-owners group is included, and empty Groups means only the
// owners approve (not the administrators).
func ReviewPolicyForLevel(orgID string, level *daemon.LevelApprover) *ReviewPolicy {
	var names []string
	if level != nil && level.Owners {
		names = append(names, models.ReviewGroupSidecarOwners)
		for _, g := range NormalizeReviewerGroups(level.Groups) {
			if g != models.ReviewGroupSidecarOwners {
				names = append(names, g)
			}
		}
	} else {
		var named []string
		if level != nil {
			named = level.Groups
		}
		names = reviewersOrDefault(named)
	}
	return reviewPolicyForNames(orgID, names, levelMinApprovals(level, len(names)))
}

// ReviewPolicyForGroups is ReviewPolicyForLevel for groups chosen at filing,
// such as the groups AI routing selected: one pending row per group, the
// admin group when there is none, and one approval releases.
func ReviewPolicyForGroups(orgID string, named []string) *ReviewPolicy {
	names := reviewersOrDefault(named)
	return reviewPolicyForNames(orgID, names, analyzerApprovalMinApprovals)
}

// ApplyLevelApprovalCount applies a level's all_groups_must_approve or
// min_approvals setting onto a policy built after routing chooses its groups.
func ApplyLevelApprovalCount(policy *ReviewPolicy, level *daemon.LevelApprover) {
	if policy == nil || level == nil {
		return
	}
	policy.MinApprovals = levelMinApprovals(level, len(policy.Groups))
}

func levelMinApprovals(level *daemon.LevelApprover, groupCount int) int {
	if level == nil || groupCount <= 0 {
		return analyzerApprovalMinApprovals
	}
	if level.AllGroupsMustApprove {
		return groupCount
	}
	if level.MinApprovals > 0 {
		return min(level.MinApprovals, groupCount)
	}
	return analyzerApprovalMinApprovals
}

func reviewPolicyForNames(orgID string, names []string, minApprovals int) *ReviewPolicy {
	groups := make([]models.ReviewGroups, 0, len(names))
	for _, groupName := range names {
		groups = append(groups, models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     orgID,
			GroupName: groupName,
			Status:    models.ReviewStatusPending,
		})
	}
	return &ReviewPolicy{Groups: groups, MinApprovals: minApprovals}
}
