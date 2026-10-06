package reviewapi

import (
	"fmt"
	"slices"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/utils"
)

// refuseExpiredSidecarDecision answers a decision on a sidecar review read as
// EXPIRED. Only a caller who may decide it records the expiry.
func refuseExpiredSidecarDecision(ctx *storagev2.Context, rev *models.Review, status models.ReviewStatusType) error {
	// The same order as validateReviewStatusTransition.
	switch status {
	case models.ReviewStatusApproved, models.ReviewStatusRejected, models.ReviewStatusRevoked:
	default:
		return ErrUnknownStatus
	}
	if !maySettleSidecarReview(ctx, rev) {
		return ErrExpired
	}
	return expireSidecarDecision(rev.OrgID, rev.ID, ctx.UserEmail)
}

// maySettleSidecarReview: the owner of a sidecar review is the sidecar, never a
// user, so only an admin or a member of a review or force group qualifies.
func maySettleSidecarReview(ctx *storagev2.Context, rev *models.Review) bool {
	if ctx.IsAdmin() {
		return true
	}
	for _, rg := range rev.ReviewGroups {
		if slices.Contains(ctx.UserGroups, rg.GroupName) {
			return true
		}
	}
	return utils.SlicesFindFirstIntersection(ctx.UserGroups, rev.ForceApprovalGroups) != nil
}

// expireSidecarDecision settles a decision that found the review lapsed or lost
// its write. ErrExpired when the row is or becomes EXPIRED, else ErrWrongState.
func expireSidecarDecision(orgID, reviewID, by string) error {
	expired, status, err := models.ExpireSidecarReview(models.DB, orgID, reviewID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("failed expiring review, reason=%v", err)
	}
	if status != models.ReviewStatusExpired {
		return ErrWrongState
	}
	if expired {
		log.With("review-id", reviewID, "by", by).Infof("recorded the expiry of a sidecar review")
		PublishSidecarExpiry(orgID, []string{reviewID})
	}
	return ErrExpired
}

// PublishSidecarExpiry rewrites the tracked Slack messages of expired sidecar
// reviews. It reads each review again, so the groups shown are the stored ones.
func PublishSidecarExpiry(orgID string, reviewIDs []string) {
	if slackservice.GetServiceInstance(orgID) == nil {
		return
	}
	for _, id := range reviewIDs {
		rev, err := models.GetReviewByIdOrSid(orgID, id)
		if err != nil {
			log.With("review-id", id).Warnf("failed loading the expired review for slack, reason=%v", err)
			continue
		}
		if err := UpdateSlackMessage(rev); err != nil {
			log.With("review-id", id).Warnf("failed updating slack review, reason=%v", err)
		}
	}
}
