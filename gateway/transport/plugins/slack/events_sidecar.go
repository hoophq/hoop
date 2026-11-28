package slack

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const slackAPITimeout = 10 * time.Second

// Refusals a click on a sidecar review can answer with. Each one names what
// the user or an admin can do: log in once, or change the Users page.
const (
	cpNotLinkedMsg = "Hoop could not read the email of your Slack user. " +
		"Ask an admin to add the users:read.email scope to the Slack app, " +
		"or to set your Slack ID on the Users page."
	cpNotVerifiedMsg     = "Hoop could not verify your Slack user. Try again."
	cpNoUsersScopeMsg    = "Hoop could not verify your Slack user. Ask an admin to add the users:read scope to the Slack app and reinstall it."
	cpLookupFailedMsg    = "failed obtaining approver's information"
	cpGroupsFailedMsg    = "failed obtaining approver's groups"
	cpInactiveMsg        = "Your Hoop user is not active. Ask an admin to reactivate it on the Users page."
	cpDeactivatedMsg     = "Your Slack user is deactivated."
	cpBotMsg             = "A bot cannot approve a request."
	cpGuestMsg           = "Slack guests cannot approve a request."
	cpOtherWorkspaceMsg  = "Users from another Slack workspace cannot approve a request."
	cpUnconfirmedMsg     = "Confirm the email of your Slack user before you approve a request."
	cpNoUserMsgFormat    = "No Hoop user has the email %s. Log in to Hoop once, or ask an admin to add you on the Users page."
	cpAmbiguousMsgFormat = "More than one Hoop user has the email %s. Ask an admin to fix it on the Users page."
	cpNotInGroupFormat   = "You do not belong to group %q. If you joined it recently, log in to Hoop to refresh your groups."
	cpNotOwnerMsg        = "You are not an owner of this Sidecar."
)

// resolveSidecarApprover names the approver of a click on a sidecar review.
//
// The Slack ID link comes first: an admin sets it on the Users page for a user
// whose Slack email differs. Otherwise the email Slack holds for the clicking
// user names them. Their groups are the hoop user's, which each login syncs
// from the identity provider.
//
// Slack vouches for the clicking user on both paths: a link does not outlive
// the Slack account being deactivated, made a guest, or moved out of the
// workspace.
func (p *slackPlugin) resolveSidecarApprover(ev *event, rev *models.Review) *storagev2.Context {
	sid := ev.msg.SessionID
	linked, err := models.GetUserByOrgIDAndSlackID(ev.orgID, ev.msg.SlackID)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver by slack id, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpLookupFailedMsg)
		return nil
	}
	slackUser := readSlackUser(ev, linked != nil)
	if slackUser == nil {
		return nil
	}
	if linked != nil {
		if !slices.Contains(models.ApproverStatuses, linked.Status) {
			log.With("sid", sid).Infof("refused slack user %s: linked user %s is %s",
				ev.msg.SlackID, linked.Email, linked.Status)
			_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpInactiveMsg)
			return nil
		}
		return p.sidecarApproverContext(ev, rev, linked)
	}
	return p.resolveEmailApprover(ev, rev, slackUser)
}

// readSlackUser reads the clicking user from Slack and checks that Slack
// vouches for them. Returns nil after telling the Slack user why the click was
// refused. linked is true when a hoop user holds the Slack ID: the email is
// not needed then.
func readSlackUser(ev *event, linked bool) *slackservice.SlackUser {
	sid := ev.msg.SessionID
	ctx, cancel := context.WithTimeout(context.Background(), slackAPITimeout)
	defer cancel()
	slackUser, err := ev.ss.GetUserInfo(ctx, ev.msg.SlackID)
	if err != nil {
		// Only a missing scope means the org has not updated its Slack app;
		// anything else is Slack failing, and a retry may work.
		msg := cpNotVerifiedMsg
		switch {
		case slackservice.IsMissingScope(err) && linked:
			msg = cpNoUsersScopeMsg
		case slackservice.IsMissingScope(err):
			msg = cpNotLinkedMsg
		}
		log.With("sid", sid).Warnf("failed reading slack user %s, reason=%v", ev.msg.SlackID, err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", msg)
		return nil
	}
	refusal := checkSlackIdentity(slackUser, ev.ss.TeamID(), ev.ss.EnterpriseID())
	if refusal == "" && !linked {
		refusal = checkSlackEmail(slackUser)
	}
	if refusal != "" {
		log.With("sid", sid).Infof("refused slack user %s: %s", ev.msg.SlackID, refusal)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", refusal)
		return nil
	}
	return slackUser
}

// resolveEmailApprover names the approver by the email Slack holds for the
// clicking user, whom readSlackUser already checked. Returns nil after
// telling the Slack user why the click was refused.
func (p *slackPlugin) resolveEmailApprover(ev *event, rev *models.Review, slackUser *slackservice.SlackUser) *storagev2.Context {
	sid := ev.msg.SessionID
	users, err := models.ListApproverUsersByEmailAndOrg(models.DB, ev.orgID, slackUser.Email)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver by email, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpLookupFailedMsg)
		return nil
	}
	approver, refusal := pickApprover(users, slackUser.Email, p.loginURL())
	if refusal != "" {
		log.With("sid", sid).Infof("refused slack user %s (%s): %s", ev.msg.SlackID, slackUser.Email, refusal)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", refusal)
		return nil
	}
	return p.sidecarApproverContext(ev, rev, approver)
}

// checkSlackUser returns why Slack does not vouch for the clicking user, or ""
// when it does. The identity checks come before the empty email: a deleted
// user, a bot or a guest is refused whatever scopes the app has.
func checkSlackUser(u *slackservice.SlackUser, botTeamID, botEnterpriseID string) string {
	if refusal := checkSlackIdentity(u, botTeamID, botEnterpriseID); refusal != "" {
		return refusal
	}
	return checkSlackEmail(u)
}

// checkSlackIdentity refuses a deleted user, a bot, a guest, and a user from
// outside the workspace. It applies to linked users too.
func checkSlackIdentity(u *slackservice.SlackUser, botTeamID, botEnterpriseID string) string {
	switch u.IdentityRefusal(botTeamID, botEnterpriseID) {
	case slackservice.RefusalDeactivated:
		return cpDeactivatedMsg
	case slackservice.RefusalBot:
		return cpBotMsg
	case slackservice.RefusalGuest:
		return cpGuestMsg
	case slackservice.RefusalOtherWorkspace:
		return cpOtherWorkspaceMsg
	}
	return ""
}

// checkSlackEmail refuses an email hoop cannot match a user by.
func checkSlackEmail(u *slackservice.SlackUser) string {
	switch {
	case u.Email == "":
		// Slack answers an empty email when the app lacks users:read.email.
		return cpNotLinkedMsg
	case !u.IsEmailConfirmed:
		return cpUnconfirmedMsg
	}
	return ""
}

// pickApprover requires exactly one hoop user for the email. More than one
// means hoop cannot tell which person clicked, so it refuses rather than
// guess. loginURL, when set, goes under the refusal a login fixes.
func pickApprover(users []models.User, email, loginURL string) (*models.User, string) {
	switch len(users) {
	case 0:
		return nil, withLoginLink(fmt.Sprintf(cpNoUserMsgFormat, email), loginURL)
	case 1:
		return &users[0], ""
	default:
		return nil, fmt.Sprintf(cpAmbiguousMsgFormat, email)
	}
}

// sidecarApproverContext checks that the hoop user belongs to the clicked
// group and builds the context DoReview reads. SlackID is the clicking user's,
// which is the one a link on the Users page would hold anyway.
func (p *slackPlugin) sidecarApproverContext(ev *event, rev *models.Review, approver *models.User) *storagev2.Context {
	sid := ev.msg.SessionID
	rows, err := models.GetUserGroupsByUserID(approver.ID)
	if err != nil {
		log.With("sid", sid).Errorf("failed obtaining approver's groups, err=%v", err)
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpGroupsFailedMsg)
		return nil
	}
	groups := make([]string, 0, len(rows))
	for _, g := range rows {
		if g.Name != models.ReviewGroupSidecarOwners {
			groups = append(groups, g.Name)
		}
	}
	if ev.msg.GroupName == models.ReviewGroupSidecarOwners {
		sidecarID := ""
		if rev != nil && rev.SidecarID.Valid {
			sidecarID = rev.SidecarID.String
		}
		ok, err := models.IsSidecarOwner(models.DB, ev.orgID, sidecarID, approver.Email)
		if err != nil {
			log.With("sid", sid).Errorf("failed checking sidecar ownership for %s, err=%v", approver.Email, err)
			_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpLookupFailedMsg)
			return nil
		}
		if !ok {
			log.With("sid", sid).Infof("approver %s is not an owner of sidecar %s", approver.Email, sidecarID)
			_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", cpNotOwnerMsg)
			return nil
		}
	} else if !slices.Contains(groups, ev.msg.GroupName) {
		log.With("sid", sid).Infof("approver %s is not on group %q", approver.Email, ev.msg.GroupName)
		msg := withLoginLink(fmt.Sprintf(cpNotInGroupFormat, ev.msg.GroupName), p.loginURL())
		_ = ev.ss.PostEphemeralMessage(ev.msg, "%s", msg)
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

// loginURL is the login page, where a login creates the hoop user or
// refreshes their groups. Empty when the plugin has no API URL.
func (p *slackPlugin) loginURL() string {
	if p.apiURL == "" {
		return ""
	}
	return strings.TrimSuffix(p.apiURL, "/") + "/login"
}

// withLoginLink puts the login URL under a refusal, as the gateway puts its
// association link.
func withLoginLink(msg, loginURL string) string {
	if loginURL == "" {
		return msg
	}
	return msg + "\n" + loginURL
}
