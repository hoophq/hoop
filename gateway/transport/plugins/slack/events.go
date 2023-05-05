package slack

import (
	"fmt"
	"strings"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/review/jit"
	slackservice "github.com/runopsio/hoop/gateway/slack"
	"github.com/runopsio/hoop/gateway/user"
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
	// ctx := &user.Context{&user.Org{Id: ev.orgID}, &user.User{SlackID: ev.msg.SlackID}}
	// slackApprover, err := p.userSvc.FindByGroups(ctx, []string{})
	slackApprover, err := p.userSvc.FindBySlackID(&user.Org{Id: ev.orgID}, ev.msg.SlackID)
	if err != nil {
		log.With("session", sid).Errorf("failed obtaning approver information, err=%v", err)
		_ = ev.ss.OpenModalError(ev.msg, "failed obtaining approver's information")
		return
	}
	if slackApprover == nil {
		log.With("session", sid).Infof("approver is not allowed")
		_ = ev.ss.OpenModalError(ev.msg, "approver is not allowed")
		return
	}
	if !pb.IsInList(ev.msg.GroupName, slackApprover.Groups) {
		log.With("session", sid).Infof("approver not allowed, it does not belong to %s", ev.msg.GroupName)
		_ = ev.ss.OpenModalError(ev.msg, "approver does not belong to this group")
		return
	}
	log.With("session", sid).Infof("found a valid approver user=%s, slackid=%s",
		slackApprover.Email, ev.msg.SlackID)
	userContext := &user.Context{
		Org: &user.Org{Id: ev.orgID},
		User: &user.User{
			Id:     slackApprover.Id,
			Groups: slackApprover.Groups,
		}}

	// perform the review in the system
	log.With("session", sid).Infof("performing review, kind=%v, id=%v, status=%s, group=%v",
		ev.msg.EventKind, ev.msg.ID, ev.msg.Status, ev.msg.GroupName)
	switch ev.msg.EventKind {
	case slackservice.EventKindReview:
		status := review.StatusRejected
		if ev.msg.Status == "approved" {
			status = review.StatusApproved
		}
		p.performExecReview(ev, userContext, status)
	case slackservice.EventKindJit:
		status := jit.StatusRejected
		if ev.msg.Status == "approved" {
			status = jit.StatusApproved
		}
		p.performJitReview(ev, userContext, status)
	default:
		log.With("session", sid).Warnf("received unknown event kind %v", ev.msg.EventKind)
	}
}

func (p *slackPlugin) performExecReview(ev *event, ctx *user.Context, status review.Status) {
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
		// TODO: check if the error came from gRPC transport!
		isApproved := rev.Status == review.StatusApproved
		err = ev.ss.UpdateMessage(ev.msg, isApproved)
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

func (p *slackPlugin) performJitReview(ev *event, ctx *user.Context, status jit.Status) {
	j, err := p.jitSvc.Review(ctx, ev.msg.ID, status)
	sid := ev.msg.SessionID
	switch err {
	case jit.ErrWrongState, jit.ErrNotFound:
		status := "not-found"
		if j != nil {
			status = strings.ToLower(string(j.Status))
		}
		err = ev.ss.UpdateMessageStatus(ev.msg, fmt.Sprintf("• _jit has already been `%s`_", status))
	case nil:
		// TODO: check if the error came from gRPC transport!
		isApproved := j.Status == jit.StatusApproved
		err = ev.ss.UpdateMessage(ev.msg, isApproved)
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