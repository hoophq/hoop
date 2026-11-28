package slack

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// linkTimeout bounds one link attempt: the database reads and the Slack call.
const linkTimeout = 10 * time.Second

// LinkUserByEmail links the hoop user that has this email to the Slack user
// that has the same one, and returns the Slack ID the hoop user has
// afterwards, "" when none. It does nothing unless the org turned
// featureflag.FlagSlackLookupByEmail on and runs the Slack integration.
//
// Nothing is guessed. Two hoop users with the email, a Slack user that Slack
// does not vouch for, an unconfirmed email, and a Slack ID that another hoop
// user has, link nobody. A hoop user with a Slack ID keeps it.
func LinkUserByEmail(ctx context.Context, db *gorm.DB, orgID, email string) (string, error) {
	if email == "" || !featureflag.IsEnabled(orgID, featureflag.FlagSlackLookupByEmail) {
		return "", nil
	}
	svc := GetServiceInstance(orgID)
	if svc == nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, linkTimeout)
	defer cancel()
	db = db.WithContext(ctx)

	users, err := models.ListApproverUsersByEmailAndOrg(db, orgID, email)
	if err != nil {
		return "", fmt.Errorf("failed reading the user to link, err=%w", err)
	}
	if len(users) != 1 {
		return "", nil
	}
	if users[0].SlackID != "" {
		return users[0].SlackID, nil
	}

	slackUser, err := svc.LookupUserByEmail(ctx, email)
	switch {
	case IsUserNotFound(err):
		return "", nil
	case err != nil:
		return "", err
	}
	switch {
	case slackUser.IdentityRefusal(svc.TeamID(), svc.EnterpriseID()) != Vouched,
		!slackUser.IsEmailConfirmed,
		!strings.EqualFold(slackUser.Email, email):
		log.With("org", orgID).Infof("slack user %s is not linked to %s: slack does not vouch for it", slackUser.ID, email)
		return "", nil
	}

	linked, err := models.LinkUserSlackID(db, orgID, users[0].ID, slackUser.ID)
	if err != nil {
		return "", fmt.Errorf("failed storing the slack id, err=%w", err)
	}
	if !linked {
		log.With("org", orgID).Infof("slack user %s is not linked to %s: another user has it, or the user was linked meanwhile", slackUser.ID, email)
		return "", nil
	}
	log.With("org", orgID).Infof("linked slack user %s to %s by email", slackUser.ID, email)
	return slackUser.ID, nil
}

// LinkUserAfterLogin is LinkUserByEmail for a login handler: it runs detached
// from the request and logs a failure, because a login must not wait for
// Slack or fail with it.
func LinkUserAfterLogin(orgID, email string) {
	if _, err := LinkUserByEmail(context.Background(), models.DB, orgID, email); err != nil {
		log.With("org", orgID).Warnf("failed linking the slack user of %s by email, reason=%v", email, err)
	}
}
