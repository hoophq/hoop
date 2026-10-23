package reviewapi

import (
	"fmt"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// notifySidecarDecision sends the person a sidecar review is filed under a
// direct message with the decision. Nothing is sent while the flag is off,
// when the filing resolved no hoop user, or when that user has no linked
// Slack ID. The Slack ID is read now, not at filing: a link made since then
// counts.
func notifySidecarDecision(rev *models.Review, reviewer string) {
	if !services.SidecarReviewOwnerEnabled(rev.OrgID) {
		return
	}
	slackSvc := slackservice.GetServiceInstance(rev.OrgID)
	if slackSvc == nil {
		return
	}
	// The sidecar itself owns a review filed under nobody.
	if rev.OwnerID == "" || rev.OwnerID == rev.SidecarID.String {
		return
	}
	person, err := services.ResolveSidecarPerson(models.DB, rev.OrgID, rev.OwnerEmail, nil)
	if err != nil {
		log.With("sid", rev.SessionID, "review-id", rev.ID).
			Warnf("failed resolving the person to message about the decision, reason=%v", err)
		return
	}
	if person.SlackID == "" {
		return
	}
	text, ok := sidecarDecisionMessage(rev, reviewer)
	if !ok {
		return
	}
	if err := slackSvc.PostMessage(person.SlackID, text); err != nil {
		log.With("sid", rev.SessionID, "review-id", rev.ID).
			Warnf("failed sending the decision DM to %s, reason=%v", person.Email, err)
	}
}

// sidecarDecisionMessage is the DM text for a decided review; false for a
// status that is not a decision.
func sidecarDecisionMessage(rev *models.Review, reviewer string) (string, bool) {
	connection := rev.ConnectionName
	if connection == "" {
		connection = rev.ListenerName.String
	}
	url := fmt.Sprintf("%s/approvals/%s", appconfig.Get().FullApiURL(), rev.SessionID)
	switch rev.Status {
	case models.ReviewStatusApproved:
		return slackservice.SidecarReviewApprovedMessage(connection, url), true
	case models.ReviewStatusRejected:
		return slackservice.SidecarReviewDeniedMessage(connection, reviewer, ptr.ToString(rev.RejectionReason), url), true
	}
	return "", false
}

// reviewerName is how the decision DM names who decided.
func reviewerName(ctx *storagev2.Context) string {
	if ctx.UserName != "" {
		return ctx.UserName
	}
	return ctx.UserEmail
}
