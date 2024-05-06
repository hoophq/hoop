package slack

import (
	"fmt"
	"strings"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	"github.com/runopsio/hoop/gateway/review"
	slackservice "github.com/runopsio/hoop/gateway/slack"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type event struct {
	ss    *slackservice.SlackService
	msg   *slackservice.MessageReviewResponse
	orgID string
}

func (p *slackPlugin) processEventResponse(ev *event) {
	sid := ev.msg.SessionID
	log.With("session", sid).Infof("received message response, review=%v, status=%v",
		ev.msg.ID, ev.msg.Status)

	// validate if the slack user is able to review it
	orgCtx := pgrest.NewOrgContext(ev.orgID)
	slackApprover, err := pgusers.New().FetchOneBySlackID(orgCtx, ev.msg.SlackID)
	if err != nil {
		log.With("session", sid).Errorf("failed obtaning approver information, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "failed obtaining approver's information")
		return
	}
	if slackApprover == nil {
		log.With("session", sid).Infof("approver is not allowed")
		_ = ev.ss.PostEphemeralMessage(ev.msg, fmt.Sprintf("You are not registered. "+
			"Please click on this link to integrate your slack user with your user hoop.\n"+
			"%s/slack/user/new/%s", p.idpProvider.ApiURL, ev.msg.SlackID))
		return
	}
	if !pb.IsInList(ev.msg.GroupName, slackApprover.Groups) {
		log.With("session", sid).Infof("approver not allowed, it does not belong to %s", ev.msg.GroupName)
		_ = ev.ss.PostEphemeralMessage(ev.msg, fmt.Sprintf("You can't review this session for group %s"+
			" because you do not belong to this group", ev.msg.GroupName))
		return
	}
	log.With("session", sid).Infof("found a valid approver user=%s, slackid=%s",
		slackApprover.Email, ev.msg.SlackID)
	userContext := storagev2.NewContext(slackApprover.Id, ev.orgID)
	userContext.UserGroups = slackApprover.Groups

	// perform the review in the system
	log.With("session", sid).Infof("performing review, kind=%v, id=%v, status=%s, group=%v",
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
		log.With("session", sid).Warnf("received unknown event kind %v", ev.msg.EventKind)
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

		if isApproved {
			ev.ss.PostMessage(rev.ReviewOwner.SlackID, fmt.Sprintf("Your session is ready to be executed.\n"+
				"Please follow this link to execute it: "+
				"%s/sessions/%s", p.idpProvider.ApiURL, ev.msg.SessionID))
		}

		log.With("session", sid).Infof("review id=%s, status=%v", ev.msg.ID, rev.Status)
	default:
		log.With("session", sid).Warnf("failed reviewing, id=%s, internal error=%v",
			ev.msg.ID, err)
		err = ev.ss.OpenModalError(ev.msg, err.Error())
	}
	if err != nil {
		log.With("session", sid).Warnf("failed updating slack review, reason=%v", err)
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
			ev.ss.PostMessage(j.ReviewOwner.SlackID, fmt.Sprintf("Your interactive session is ready to be executed.\n"+
				"Please follow this link to execute it: "+
				"%s/sessions/%s", p.idpProvider.ApiURL, ev.msg.SessionID))
		}

		log.With("session", sid).Infof("jit review id=%s, status=%v", ev.msg.ID, j.Status)
	default:
		log.With("session", sid).Warnf("failed reviewing jit, id=%s, internal error=%v",
			ev.msg.ID, err)
		err = ev.ss.OpenModalError(ev.msg, err.Error())
	}
	if err != nil {
		log.With("session", sid).Warnf("failed updating slack jit review, reason=%v", err)
	}
}
