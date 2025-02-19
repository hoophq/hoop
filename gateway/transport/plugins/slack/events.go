package slack

import (
	"fmt"
	"strings"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
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
		_ = ev.ss.PostEphemeralMessage(ev.msg, fmt.Sprintf("You are not registered. "+
			"Visit the link to associate your Slack user with Hoop.\n"+
			"%s/slack/user/new/%s", p.idpProvider.ApiURL, ev.msg.SlackID))
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
	if !pb.IsInList(ev.msg.GroupName, slackApproverGroupsList) {
		log.With("sid", sid).Infof("approver not allowed, it does not belong to %s", ev.msg.GroupName)
		_ = ev.ss.PostEphemeralMessage(ev.msg,
			fmt.Sprintf("You can't review this session for group %s because you do not belong to this group",
				ev.msg.GroupName))
		return
	}
	log.With("sid", sid).Infof("found a valid approver user=%s, slackid=%s",
		slackApprover.Email, ev.msg.SlackID)
	userContext := storagev2.NewContext(slackApprover.ID, ev.orgID)
	userContext.UserGroups = slackApproverGroupsList

	// perform the review in the system
	log.With("sid", sid).Infof("performing review, kind=%v, id=%v, status=%s, group=%v",
		ev.msg.EventKind, ev.msg.ID, ev.msg.Status, ev.msg.GroupName)
	switch ev.msg.EventKind {
	case slackservice.EventKindOneTime:
		status := types.ReviewStatusRejected
		if ev.msg.Status == "approved" {
			status = types.ReviewStatusApproved
		}
		p.performExecReview(ev, userContext, status)
	case slackservice.EventKindJit:
		status := types.ReviewStatusRejected
		if ev.msg.Status == "approved" {
			status = types.ReviewStatusApproved
		}
		p.performJitReview(ev, userContext, status)
	default:
		log.With("sid", sid).Warnf("received unknown event kind %v", ev.msg.EventKind)
	}
}

func (p *slackPlugin) performExecReview(ev *event, ctx *storagev2.Context, status types.ReviewStatus) {
	rev, err := p.reviewSvc.Review(ctx, ev.msg.ID, status)
	sid := ev.msg.SessionID
	switch err {
	case review.ErrWrongState, review.ErrNotFound:
		status := "not-found"
		if rev != nil {
			status = strings.ToLower(string(rev.Status))
		}
		err = ev.ss.UpdateMessageStatus(ev.msg, fmt.Sprintf("• _review has already been `%s`_", status))
	case nil:
		isApproved := rev.Status == types.ReviewStatusApproved
		err = ev.ss.UpdateMessage(ev.msg, isApproved)

		log.With("sid", sid).Infof("review id=%s, isapproved=%v, status=%v, update-msg-err=%v",
			ev.msg.ID, isApproved, rev.Status, err)
	default:
		log.With("sid", sid).Warnf("failed reviewing, id=%s, internal error=%v",
			ev.msg.ID, err)
		err = ev.ss.OpenModalError(ev.msg, err.Error())
	}
	if err != nil {
		log.With("sid", sid).Warnf("failed updating slack review, reason=%v", err)
	}
}

func (p *slackPlugin) performJitReview(ev *event, ctx *storagev2.Context, status types.ReviewStatus) {
	j, err := p.reviewSvc.Review(ctx, ev.msg.ID, status)
	sid := ev.msg.SessionID
	switch err {
	case review.ErrWrongState, review.ErrNotFound:
		status := "not-found"
		if j != nil {
			status = strings.ToLower(string(j.Status))
		}
		err = ev.ss.UpdateMessageStatus(ev.msg, fmt.Sprintf("• _jit has already been `%s`_", status))
	case nil:
		isApproved := j.Status == types.ReviewStatusApproved
		err = ev.ss.UpdateMessage(ev.msg, isApproved)

		if isApproved {
			ev.ss.PostMessage(j.ReviewOwner.SlackID,
				fmt.Sprintf("Your interactive session is open.\n"+
					"Follow this link to see the details: %s/sessions/%s", p.idpProvider.ApiURL, ev.msg.SessionID))
		}

		log.With("sid", sid).Infof("jit review id=%s, status=%v", ev.msg.ID, j.Status)
	default:
		log.With("sid", sid).Warnf("failed reviewing jit, id=%s, internal error=%v",
			ev.msg.ID, err)
		err = ev.ss.OpenModalError(ev.msg, err.Error())
	}
	if err != nil {
		log.With("sid", sid).Warnf("failed updating slack jit review, reason=%v", err)
	}
}
