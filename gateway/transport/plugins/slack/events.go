package slack

import (
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/review"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
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
			"%s/slack/user/new/%s", p.idpProvider.ApiURL, ev.msg.SlackID)
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
		status := types.ReviewStatusRejected
		if ev.msg.Status == "approved" {
			status = types.ReviewStatusApproved
		}
		p.performReview(ev, userContext, status)
	default:
		log.With("sid", sid).Warnf("received unknown event kind %v", ev.msg.EventKind)
	}
}

func (p *slackPlugin) performReview(ev *event, ctx *storagev2.Context, status types.ReviewStatus) {
	rev, err := p.reviewSvc.Review(ctx, ev.msg.ID, status)
	var msg string
	switch err {
	case review.ErrNotFound:
		msg = err.Error()
	case review.ErrWrongState:
		msg = "The review is already approved or rejected"
	case review.ErrSelfApproval:
		msg = "Unable to self approval review, contact another member of you team to approve it"
	case review.ErrNotEligible:
		msg = "You're not eligible to approve/reject this review"
	case nil:
		isApproved := rev.Status == types.ReviewStatusApproved
		err = ev.ss.UpdateMessage(ev.msg, isApproved)
		log.With("sid", ev.msg.SessionID).Infof("review id=%s, isapproved=%v, status=%v, update-msg-err=%v",
			ev.msg.ID, isApproved, rev.Status, err)
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
