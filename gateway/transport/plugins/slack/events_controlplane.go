package slack

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const slackAPITimeout = 10 * time.Second

// Refusals a control plane click can answer with. The gateway's association
// page does not exist in the control plane web app, so each one names what an
// admin can change: the Users page or Settings -> Provisioning.
const (
	cpNotLinkedMsg = "Hoop could not read the email of your Slack user. " +
		"Ask an admin to import you from Slack in Settings -> Provisioning, " +
		"or to set your Slack ID on the Users page."
	cpNotVerifiedMsg     = "Hoop could not verify your Slack user. Try again."
	cpLookupFailedMsg    = "failed obtaining approver's information"
	cpGroupsFailedMsg    = "failed obtaining approver's groups"
	cpInactiveMsg        = "Your Hoop user is not active. Ask an admin to reactivate it on the Users page."
	cpDeactivatedMsg     = "Your Slack user is deactivated."
	cpBotMsg             = "A bot cannot approve a review."
	cpGuestMsg           = "Slack guests cannot approve a review."
	cpOtherWorkspaceMsg  = "Users from another Slack workspace cannot approve a review."
	cpUnconfirmedMsg     = "Confirm the email of your Slack user before approving a review."
	cpNoUserMsgFormat    = "No Hoop user has the email %s. Ask an admin to add you on the Users page or in Settings -> Provisioning."
	cpAmbiguousMsgFormat = "More than one Hoop user has the email %s. Ask an admin to fix it on the Users page."
	cpNotInGroupFormat   = "You do not belong to group %q. Ask an admin to add you on the Users page or in Settings -> Provisioning."
)

// resolveControlPlaneApprover names the approver of a control plane click.
//
// The Slack ID link comes first: a Slack import writes it for every member it
// provisions, and so does an admin on the Users page. The email Slack holds for
// the clicking user is the fallback, for users added by hand who were never
// imported from Slack. Nobody has to log in for either.
func (p *slackPlugin) resolveControlPlaneApprover(ev *event) *storagev2.Context {
	sid := ev.msg.SessionID
	linked, err := models.GetUserByOrgIDAndSlackID(ev.orgID, ev.msg.SlackID)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver by slack id, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpLookupFailedMsg)
		return nil
	}
	if linked != nil {
		if !slices.Contains(models.ApproverStatuses, linked.Status) {
			log.With("sid", sid).Infof("refused slack user %s: linked user %s is %s",
				ev.msg.SlackID, linked.Email, linked.Status)
			_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpInactiveMsg)
			return nil
		}
		return p.controlPlaneApproverContext(ev, linked)
	}
	return p.resolveEmailApprover(ev)
}

// resolveEmailApprover names the approver by the email Slack holds for the
// clicking user. Returns nil after telling the Slack user why the click was
// refused.
func (p *slackPlugin) resolveEmailApprover(ev *event) *storagev2.Context {
	sid := ev.msg.SessionID
	ctx, cancel := context.WithTimeout(context.Background(), slackAPITimeout)
	defer cancel()
	slackUser, err := ev.ss.GetUserInfo(ctx, ev.msg.SlackID)
	if err != nil {
		// Only a missing scope means the org has not updated its Slack app;
		// anything else is Slack failing, and a retry may work.
		msg := cpNotVerifiedMsg
		if slackservice.IsMissingScope(err) {
			msg = cpNotLinkedMsg
		}
		log.With("sid", sid).Warnf("failed reading slack user %s, reason=%v", ev.msg.SlackID, err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", msg)
		return nil
	}

	if refusal := checkSlackUser(slackUser, ev.ss.TeamID(), ev.ss.EnterpriseID()); refusal != "" {
		log.With("sid", sid).Infof("refused slack user %s: %s", ev.msg.SlackID, refusal)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", refusal)
		return nil
	}

	users, err := models.ListApproverUsersByEmailAndOrg(models.DB, ev.orgID, slackUser.Email)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver by email, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpLookupFailedMsg)
		return nil
	}
	approver, refusal := pickApprover(users, slackUser.Email)
	if refusal != "" {
		log.With("sid", sid).Infof("refused slack user %s (%s): %s", ev.msg.SlackID, slackUser.Email, refusal)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", refusal)
		return nil
	}
	return p.controlPlaneApproverContext(ev, approver)
}

// checkSlackUser returns why Slack does not vouch for the clicking user, or ""
// when it does. The identity checks come before the empty email: a deleted
// user, a bot or a guest is refused whatever scopes the app has.
//
// botTeamID and botEnterpriseID are the workspace and grid the bot token
// belongs to. A user of another workspace in the same Enterprise Grid org is
// accepted; one from outside it is not.
func checkSlackUser(u *slackservice.SlackUser, botTeamID, botEnterpriseID string) string {
	switch {
	case u.Deleted:
		return cpDeactivatedMsg
	case u.IsBot:
		return cpBotMsg
	case u.IsRestricted || u.IsUltraRestricted:
		return cpGuestMsg
	case u.IsStranger:
		return cpOtherWorkspaceMsg
	case !sameWorkspace(u, botTeamID, botEnterpriseID):
		return cpOtherWorkspaceMsg
	case u.Email == "":
		// Slack answers an empty email when the app lacks users:read.email.
		return cpNotLinkedMsg
	case !u.IsEmailConfirmed:
		return cpUnconfirmedMsg
	}
	return ""
}

func sameWorkspace(u *slackservice.SlackUser, botTeamID, botEnterpriseID string) bool {
	if botTeamID == "" || u.TeamID == "" || u.TeamID == botTeamID {
		return true
	}
	return botEnterpriseID != "" && u.EnterpriseID == botEnterpriseID
}

// pickApprover requires exactly one hoop user for the email. More than one
// means hoop cannot tell which person clicked, so it refuses rather than
// guess.
func pickApprover(users []models.User, email string) (*models.User, string) {
	switch len(users) {
	case 0:
		return nil, fmt.Sprintf(cpNoUserMsgFormat, email)
	case 1:
		return &users[0], ""
	default:
		return nil, fmt.Sprintf(cpAmbiguousMsgFormat, email)
	}
}

// controlPlaneApproverContext checks that the hoop user belongs to the clicked
// group and builds the context DoReview reads. SlackID is the clicking user's,
// which is the one a link on the Users page would hold anyway.
func (p *slackPlugin) controlPlaneApproverContext(ev *event, approver *models.User) *storagev2.Context {
	sid := ev.msg.SessionID
	rows, err := models.GetUserGroupsByUserID(approver.ID)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver's groups, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpGroupsFailedMsg)
		return nil
	}
	groups := make([]string, 0, len(rows))
	for _, g := range rows {
		groups = append(groups, g.Name)
	}
	if !slices.Contains(groups, ev.msg.GroupName) {
		log.With("sid", sid).Infof("approver %s is not on group %q", approver.Email, ev.msg.GroupName)
		_ = ev.ss.PostEphemeralMessage(ev.msg, cpNotInGroupFormat, ev.msg.GroupName)
		return nil
	}

	log.With("sid", sid).Infof("found a valid approver user=%s, slackid=%s", approver.Email, ev.msg.SlackID)
	userContext := storagev2.NewContext(approver.Subject, ev.orgID)
	userContext.UserGroups = groups
	userContext.UserName = approver.Name
	userContext.UserEmail = approver.Email
	userContext.SlackID = ev.msg.SlackID
	return userContext
}
