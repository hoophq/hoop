// Package reviewescalation escalates pending sidecar reviews whose escalation
// delay has elapsed: it adds the escalation groups to private.review_groups
// and notifies their members by Slack direct message (never a channel).
package reviewescalation

import (
	"context"
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"gorm.io/gorm"
)

const (
	sweepInterval  = 15 * time.Second
	sweepTimeout   = 10 * time.Second
	sweepBatchSize = 50
)

// Run sweeps once immediately, then every sweepInterval until ctx is done.
// Every replica runs its own ticker; ClaimDueEscalations locks rows with
// FOR UPDATE SKIP LOCKED so replicas take disjoint reviews and never notify
// twice.
func Run(ctx context.Context, db *gorm.DB) {
	SweepOnce(ctx, db, time.Now().UTC())

	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			SweepOnce(ctx, db, time.Now().UTC())
		}
	}
}

// SweepOnce claims due escalations as of now and notifies their escalation
// groups by direct message. Exported for tests.
func SweepOnce(ctx context.Context, db *gorm.DB, now time.Time) int {
	ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()

	claimed, err := models.ClaimDueEscalations(db.WithContext(ctx), now, sweepBatchSize)
	if err != nil {
		log.Errorf("reviewescalation: failed claiming due escalations, reason=%v", err)
		return 0
	}
	for _, esc := range claimed {
		notifyEscalation(db, esc)
	}
	return len(claimed)
}

func notifyEscalation(db *gorm.DB, esc models.EscalatedReview) {
	svc := slackservice.GetServiceInstance(esc.OrgID)
	if svc == nil {
		return
	}
	slackIDs, err := models.ListGroupMemberSlackIDs(db, esc.OrgID, esc.Groups)
	if err != nil {
		log.With("org", esc.OrgID, "review-id", esc.ReviewID).
			Warnf("reviewescalation: failed reading slack ids, reason=%v", err)
		return
	}
	if len(slackIDs) == 0 {
		log.With("org", esc.OrgID, "review-id", esc.ReviewID).
			Infof("reviewescalation: escalated to %v, no member has a slack id", esc.Groups)
		return
	}
	connection := esc.ConnectionName
	if connection == "" {
		connection = esc.ListenerName
	}
	req := &slackservice.MessageReviewRequest{
		ID:                       esc.ReviewID,
		SessionID:                esc.SessionID,
		Name:                     esc.SidecarName,
		Email:                    esc.OwnerEmail,
		UserGroups:               esc.Groups,
		Connection:               connection,
		ConnectionType:           "sidecar",
		ApprovalGroups:           esc.Groups,
		Script:                   esc.Statement,
		WebappURL:                fmt.Sprintf("%s/approvals/%s", appconfig.Get().FullApiURL(), esc.SessionID),
		ExpiresAt:                esc.ExpiresAt,
		SlackChannels:            slackIDs,
		DefaultChannelAsFallback: true,
	}
	res := svc.PostMessageReview(req)
	log.With("org", esc.OrgID, "review-id", esc.ReviewID).
		Infof("reviewescalation: notified %d/%d members of %v", res.Posted, len(slackIDs), esc.Groups)
}
