package slack

import (
	"fmt"
	"slices"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/log"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
)

type event struct {
	ss    *slackservice.SlackService
	msg   *slackservice.MessageReviewResponse
	orgID string
}

func (p *slackPlugin) processEventResponse(ev *event) {
	sid := ev.msg.SessionID
	log.With("sid", sid).Infof("received message response, review=%v, status=%v",
		ev.msg.ID, ev.msg.Status)

	// validate if the slack user is able to review it
	slackApprover, err := models.GetUserByOrgIDAndSlackID(ev.orgID, ev.msg.SlackID)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver information, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "failed obtaining approver's information")
		return
	}

	if slackApprover == nil {
		log.With("sid", sid).Infof("approver is not allowed")
		_ = ev.ss.PostEphemeralMessage(ev.msg, "You are not registered. "+
			"Visit the link to associate your Slack user with Hoop.\n"+
			"%s/slack/user/new/%s", p.apiURL, ev.msg.SlackID)
		return
	}

	slackApproverGroups, err := models.GetUserGroupsByUserID(slackApprover.ID)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver's groups, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "failed obtaining approver's groups")
		return
	}
	var slackApproverGroupsList []string
	for _, group := range slackApproverGroups {
		slackApproverGroupsList = append(slackApproverGroupsList, group.Name)
	}

	// Check if msg.GroupName is in slackApproverGroupList
	if !slices.Contains(slackApproverGroupsList, ev.msg.GroupName) {
		log.With("sid", sid).Infof("approver is not allowed because its not on group %q", ev.msg.GroupName)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "You do not belong to group %q.", ev.msg.GroupName)
		return
	}

	log.With("sid", sid).Infof("found a valid approver user=%s, slackid=%s",
		slackApprover.Email, ev.msg.SlackID)
	userContext := storagev2.NewContext(slackApprover.Subject, ev.orgID)
	userContext.UserGroups = slackApproverGroupsList
	userContext.UserName = slackApprover.Name
	userContext.UserEmail = slackApprover.Email
	userContext.SlackID = slackApprover.SlackID

	// perform the review in the system
	log.With("sid", sid).Infof("performing review, kind=%v, id=%v, status=%s, group=%v",
		ev.msg.EventKind, ev.msg.ID, ev.msg.Status, ev.msg.GroupName)
	switch ev.msg.EventKind {
	case slackservice.EventKindOneTime, slackservice.EventKindJit:
		status := models.ReviewStatusRejected
		if ev.msg.Status == "approved" {
			status = models.ReviewStatusApproved
		}
		p.performReview(ev, userContext, status)
	default:
		log.With("sid", sid).Warnf("received unknown event kind %v", ev.msg.EventKind)
	}
}

func (p *slackPlugin) performReview(ev *event, ctx *storagev2.Context, status models.ReviewStatusType) {
	rev, err := reviewapi.DoReview(ctx, ev.msg.ID, status)
	var msg string
	switch err {
	case reviewapi.ErrNotFound:
		msg = err.Error()
	case reviewapi.ErrWrongState:
		msg = "The review is already approved or rejected"
	case reviewapi.ErrSelfApproval:
		msg = "Unable to self approval review, contact another member of you team to approve it"
	case reviewapi.ErrNotEligible:
		msg = "You're not eligible to approve/reject this review"
	case nil:
		isApproved := rev.Status == models.ReviewStatusApproved
		isStillPending := rev.Status == models.ReviewStatusPending
		if isStillPending {
			err = fmt.Errorf("user was able to approve it, but the resource is still pending")
		} else {
			err = ev.ss.UpdateMessage(ev.msg, isApproved)
		}
		log.With("sid", ev.msg.SessionID).Infof("review id=%s, isapproved=%v, status=%v, update-msg-err=%v",
			ev.msg.ID, isApproved, rev.Status, err)
		if rev.Status == models.ReviewStatusApproved || rev.Status == models.ReviewStatusRejected {
			// release any gRPC connection waiting for a review
			p.TransportReleaseConnection(
				rev.OrgID,
				rev.SessionID,
				ptr.ToString(rev.OwnerSlackID),
				rev.Status.Str(),
			)
		}
		return
	default:
		log.With("sid", ev.msg.SessionID).Warnf("failed reviewing, id=%s, internal error=%v",
			ev.msg.ID, err)
		msg = err.Error()
	}
	if err = ev.ss.PostEphemeralMessage(ev.msg, msg); err != nil {
		log.With("sid", ev.msg.SessionID).Warnf("failed updating slack review, reason=%v", err)
	}
}
