package slack

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/log"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const slackAPITimeout = 10 * time.Second

type event struct {
	ss    *slackservice.SlackService
	msg   *slackservice.MessageReviewResponse
	orgID string
}

func (p *slackPlugin) processEventResponse(ev *event) {
	sid := ev.msg.SessionID
	log.With("sid", sid).Infof("received message response, review=%v, status=%v",
		ev.msg.ID, ev.msg.Status)

	userContext := p.resolveApprover(ev)
	if userContext == nil {
		return
	}

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

// resolveApprover returns the reviewer context, or nil after the Slack user
// was told why the click was refused. The control plane names the approver by
// the email Slack holds for them; the gateway by the Slack ID a hoop user
// linked to their account.
func (p *slackPlugin) resolveApprover(ev *event) *storagev2.Context {
	if appconfig.Get().IsControlPlane() {
		return p.resolveEmailApprover(ev)
	}
	return p.resolveHoopApprover(ev, fmt.Sprintf("You are not registered. "+
		"Visit the link to associate your Slack user with Hoop.\n"+
		"%s/slack/user/new/%s", p.apiURL, ev.msg.SlackID))
}

// controlPlaneNotLinkedMsg answers a click the control plane could resolve
// neither by email nor by Slack ID. The gateway's association page does not
// exist in the control plane web app, so it names what an admin can change.
const controlPlaneNotLinkedMsg = "Hoop could not read the email of your Slack user. " +
	"Ask an admin to add the users:read.email scope to the Slack app, " +
	"or to set your Slack ID on the Users page."

// approverBySlackID resolves the hoop user linked to the clicking Slack user.
// Returns nil, false after telling the user why the click was refused;
// notRegisteredMsg is what they read when no hoop user is linked.
func (p *slackPlugin) approverBySlackID(ev *event, notRegisteredMsg string) (*models.User, bool) {
	sid := ev.msg.SessionID
	slackApprover, err := models.GetUserByOrgIDAndSlackID(ev.orgID, ev.msg.SlackID)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver information, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "failed obtaining approver's information")
		return nil, false
	}
	if slackApprover == nil {
		log.With("sid", sid).Infof("approver is not allowed")
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", notRegisteredMsg)
		return nil, false
	}
	return slackApprover, true
}

func (p *slackPlugin) resolveHoopApprover(ev *event, notRegisteredMsg string) *storagev2.Context {
	slackApprover, ok := p.approverBySlackID(ev, notRegisteredMsg)
	if !ok {
		return nil
	}
	return p.approverContext(ev, slackApprover, slackApprover.SlackID)
}

// approverContext checks that the hoop user belongs to the clicked group and
// builds the context DoReview reads. Returns nil after telling the Slack user
// why the click was refused.
func (p *slackPlugin) approverContext(ev *event, approver *models.User, slackID string) *storagev2.Context {
	sid := ev.msg.SessionID
	approverGroups, err := models.GetUserGroupsByUserID(approver.ID)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver's groups, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "failed obtaining approver's groups")
		return nil
	}
	var approverGroupsList []string
	for _, group := range approverGroups {
		approverGroupsList = append(approverGroupsList, group.Name)
	}

	if !slices.Contains(approverGroupsList, ev.msg.GroupName) {
		log.With("sid", sid).Infof("approver is not allowed because its not on group %q", ev.msg.GroupName)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "You do not belong to group %q.", ev.msg.GroupName)
		return nil
	}

	log.With("sid", sid).Infof("found a valid approver user=%s, slackid=%s",
		approver.Email, ev.msg.SlackID)
	userContext := storagev2.NewContext(approver.Subject, ev.orgID)
	userContext.UserGroups = approverGroupsList
	userContext.UserName = approver.Name
	userContext.UserEmail = approver.Email
	userContext.SlackID = slackID
	return userContext
}

// resolveEmailApprover names the approver by the email Slack holds for the
// clicking user. The hoop user with that email was provisioned from the
// identity provider, so their groups are the identity provider's and nobody
// had to link a Slack account first.
//
// When Slack cannot tell who clicked (an API error, or no email because the
// app lacks users:read.email), it falls back to the Slack ID link, which is
// how every click was resolved before. An org that has not updated its Slack
// app keeps working instead of losing its approvals.
func (p *slackPlugin) resolveEmailApprover(ev *event) *storagev2.Context {
	sid := ev.msg.SessionID
	ctx, cancel := context.WithTimeout(context.Background(), slackAPITimeout)
	defer cancel()
	slackUser, err := ev.ss.GetUserInfo(ctx, ev.msg.SlackID)
	if err != nil {
		log.With("sid", sid).Warnf("failed reading slack user %s, falling back to the slack id link, reason=%v",
			ev.msg.SlackID, err)
		return p.resolveHoopApprover(ev, controlPlaneNotLinkedMsg)
	}

	fallback, refusal := checkSlackUser(slackUser)
	switch {
	case fallback:
		log.With("sid", sid).Warnf("slack user %s has no email, the slack app may lack the users:read.email scope; "+
			"falling back to the slack id link", ev.msg.SlackID)
		return p.resolveHoopApprover(ev, controlPlaneNotLinkedMsg)
	case refusal != "":
		log.With("sid", sid).Infof("refused slack user %s: %s", ev.msg.SlackID, refusal)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", refusal)
		return nil
	}

	users, err := models.ListActiveUsersByEmailAndOrg(models.DB, ev.orgID, slackUser.Email)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver by email, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "failed obtaining approver's information")
		return nil
	}
	approver, refusal := pickApprover(users, slackUser.Email)
	if refusal != "" {
		log.With("sid", sid).Infof("refused slack user %s (%s): %s", ev.msg.SlackID, slackUser.Email, refusal)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", refusal)
		return nil
	}
	return p.approverContext(ev, approver, ev.msg.SlackID)
}

// checkSlackUser decides whether Slack vouches for the clicking user.
// fallback is true when Slack gave no email to match on; refusal is the reason
// to give when the user must not approve at all.
func checkSlackUser(u *slackservice.SlackUser) (fallback bool, refusal string) {
	switch {
	case u.Deleted:
		return false, "Your Slack user is deactivated."
	case u.IsBot:
		return false, "A bot cannot approve a review."
	case u.Email == "":
		return true, ""
	case u.IsRestricted || u.IsUltraRestricted:
		return false, "Slack guests cannot approve a review."
	case !u.IsEmailConfirmed:
		return false, "Confirm the email of your Slack user before approving a review."
	}
	return false, ""
}

// pickApprover requires exactly one hoop user for the email. None means the
// identity provider has not provisioned them; more than one means hoop cannot
// tell which person clicked, so it refuses rather than guess.
func pickApprover(users []models.User, email string) (*models.User, string) {
	switch len(users) {
	case 0:
		return nil, fmt.Sprintf("No active Hoop user has the email %s. "+
			"Ask an admin to assign the Hoop app to you in your identity provider.", email)
	case 1:
		return &users[0], ""
	default:
		return nil, fmt.Sprintf("More than one Hoop user has the email %s. Ask an admin to fix it.", email)
	}
}

func (p *slackPlugin) performReview(ev *event, ctx *storagev2.Context, status models.ReviewStatusType) {
	// DoReview rewrites every tracked review message (all channels, all
	// reviewed groups) via UpdateSlackMessage. Capture trackedness before it
	// consumes the entry on terminal states; the callback-based update below
	// is only a fallback for untracked messages (e.g. posted before a gateway
	// restart), otherwise its click-time blocks would overwrite the rewrite
	// with a stale version where only the clicked block changed.
	tracked := ev.ss.HasTrackedReviewMessages(ev.msg.ID)
	rev, err := reviewapi.DoReview(ctx, ev.msg.ID, status, nil, false, ev.msg.RejectionReason)
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

		switch {
		case tracked:
			log.With("sid", ev.msg.SessionID).Infof("review id=%s, status=%v, skipping callback update, message rewrite delegated to DoReview",
				ev.msg.ID, rev.Status)
		case isStillPending:
			// Count how many groups have approved
			approvedCount := 0
			for _, rg := range rev.ReviewGroups {
				if rg.Status == models.ReviewStatusApproved {
					approvedCount++
				}
			}

			// Update message showing partial approval
			err = ev.ss.UpdateMessagePartialApproval(ev.msg, approvedCount, len(rev.ReviewGroups))
			log.With("sid", ev.msg.SessionID).Infof("review id=%s, partial approval, approved=%d/%d, update-msg-err=%v",
				ev.msg.ID, approvedCount, len(rev.ReviewGroups), err)
		default:
			// Full approval or rejection
			err = ev.ss.UpdateMessage(ev.msg, isApproved)
			log.With("sid", ev.msg.SessionID).Infof("review id=%s, isapproved=%v, status=%v, update-msg-err=%v",
				ev.msg.ID, isApproved, rev.Status, err)
		}

		if rev.Status == models.ReviewStatusApproved || rev.Status == models.ReviewStatusRejected {
			// release any gRPC connection waiting for a review
			p.TransportReleaseConnection(
				rev.OrgID,
				rev.SessionID,
				ptr.ToString(rev.OwnerSlackID),
				rev.Status.Str(),
				ptr.ToString(rev.RejectionReason),
				rev.RejectedByEmail(),
			)
		}
		if rev.Status == models.ReviewStatusRejected {
			p.notifyOwnerRejected(ev, ctx, rev)
		}
		return
	default:
		log.With("sid", ev.msg.SessionID).Warnf("failed reviewing, id=%s, internal error=%v",
			ev.msg.ID, err)
		msg = err.Error()
	}
	if err = ev.ss.PostEphemeralMessage(ev.msg, "%s", msg); err != nil {
		log.With("sid", ev.msg.SessionID).Warnf("failed updating slack review, reason=%v", err)
	}
}

// notifyOwnerRejected sends a DM to the session owner informing them that their
// access request was rejected. Silently skipped if the owner has no Slack ID.
func (p *slackPlugin) notifyOwnerRejected(ev *event, ctx *storagev2.Context, rev *models.Review) {
	ownerSlackID := ptr.ToString(rev.OwnerSlackID)
	if ownerSlackID == "" {
		return
	}

	reviewerName := ctx.UserName
	if reviewerName == "" {
		reviewerName = ctx.UserEmail
	}

	text := fmt.Sprintf("Your access request for *%s* was *rejected* by %s.", rev.ConnectionName, reviewerName)

	if ev.msg.RejectionReason != "" {
		text += fmt.Sprintf("\n>%s", ev.msg.RejectionReason)
	}

	text += fmt.Sprintf("\nFollow this link to see the details: %s/sessions/%s", p.apiURL, rev.SessionID)

	if err := ev.ss.PostMessage(ownerSlackID, text); err != nil {
		log.With("sid", ev.msg.SessionID).Warnf("failed sending rejection DM to session owner, err=%v", err)
	}
}
