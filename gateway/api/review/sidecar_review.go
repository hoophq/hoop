package reviewapi

import (
	"fmt"
	"slices"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

// DoSidecarReview settles a review a sidecar filed against one of its
// listeners. It is a sibling of DoReview, not a branch inside it: a sidecar
// review has no connection to resolve and a policy that is fixed rather than
// configurable, so sharing doIndividualReview would mean teaching it two
// vocabularies. DoReview and doIndividualReview stay exactly as they were.
//
// The policy: one approval from an admin or an approver settles it. There are
// no reviewer groups to configure, no minimum to count and no force-approval
// groups, because the control plane has two roles and nothing to choose
// between.
//
// rev must already be loaded. Callers reach this through DoReview.
func DoSidecarReview(ctx *storagev2.Context, rev *models.Review, status models.ReviewStatusType, rejectionReason string) (*models.Review, error) {
	// UpdateReview syncs the session's status inside its transaction, and
	// private.sessions.id is a uuid, so a review with no session would fail
	// there on a cast rather than here on the thing that is actually wrong.
	if rev.SessionID == "" {
		return nil, fmt.Errorf("sidecar review %s has no session", rev.ID)
	}

	rev, err := doSidecarReview(ctx, rev, status)
	if err != nil {
		return nil, err
	}

	if rev.Status == models.ReviewStatusRejected && rejectionReason != "" {
		rev.RejectionReason = &rejectionReason
	}

	if err := models.UpdateReview(rev); err != nil {
		return nil, fmt.Errorf("failed updating review state, reason=%v", err)
	}

	if err := UpdateSlackMessage(rev); err != nil {
		log.Warnf("failed updating slack review, err=%v", err)
	}

	// No analytics and no event routing: both key on a connection this review
	// does not have. See the PR description.

	return rev, nil
}

// doSidecarReview applies the verdict in memory. Split out for the same reason
// doReview is: it takes no database and no clock, so the policy is testable on
// its own.
func doSidecarReview(ctx *storagev2.Context, rev *models.Review, status models.ReviewStatusType) (*models.Review, error) {
	if err := validateReviewStatusTransition(ctx, rev, status); err != nil {
		return nil, err
	}

	// Group names are read, never spelled: an organization can rename its admin
	// group through the environment, and a hardcoded "admin" would silently
	// name a group nobody is in.
	eligible := []string{types.GroupAdmin, types.GroupApprover}

	reviewedAt := time.Now().UTC()
	var isEligibleReviewer bool
	for i, rg := range rev.ReviewGroups {
		if !slices.Contains(ctx.UserGroups, rg.GroupName) || !slices.Contains(eligible, rg.GroupName) {
			continue
		}
		isEligibleReviewer = true
		rev.ReviewGroups[i].Status = status
		rev.ReviewGroups[i].OwnerID = ptr.String(ctx.UserID)
		rev.ReviewGroups[i].OwnerEmail = ptr.String(ctx.UserEmail)
		rev.ReviewGroups[i].OwnerName = ptr.String(ctx.UserName)
		rev.ReviewGroups[i].OwnerSlackID = ptr.String(ctx.SlackID)
		rev.ReviewGroups[i].ReviewedAt = &reviewedAt
	}
	if !isEligibleReviewer {
		return nil, ErrNotEligible
	}

	// No RevokedAt is stamped: that is the expiry of a just-in-time access
	// window, and this authorizes one statement that has already been named.
	rev.Status = status
	return rev, nil
}
