package slack

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/liveevents"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/slack-go/slack"
	"gorm.io/gorm"
)

const (
	// LinkCommandName is the word after the slash command that links a Slack
	// user: `/hoop link <code>`.
	LinkCommandName = "link"
	// linkCodeTTL is how long a code can be sent to the bot.
	linkCodeTTL = 10 * time.Minute
	// linkCodeLen with linkCodeAlphabet gives 40 bits: a guess sent through
	// Slack within the TTL does not find a live code.
	linkCodeLen = 8
	// linkCodeAlphabet has no 0, O, 1 or I: a person copies the code by hand.
	linkCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// Answers of the link command.
const (
	linkNoCodeMsg         = "Send the code that Hoop shows you: `/hoop link <code>`. To get a code, open the user menu in Hoop and select Connect Slack."
	linkInvalidMsg        = "This code is not valid, or it expired. To get a new one, open the user menu in Hoop and select Connect Slack."
	linkInUseMsg          = "This Slack user is linked to another Hoop user. Ask an admin to change it on the Users page."
	linkOtherWorkspaceMsg = "Users from another Slack workspace cannot link to Hoop."
	linkFailedMsg         = "Hoop could not link your Slack user. Try again."
	linkDoneMsgFormat     = "Your Slack user is now linked to the Hoop user %s."
)

// LinkAvailable reports whether a user of the org can link a Slack user with
// a one-time code: the flag is on and the Slack integration runs.
func LinkAvailable(orgID string) bool {
	return featureflag.IsEnabled(orgID, featureflag.FlagSlackConnectCode) && GetServiceInstance(orgID) != nil
}

// NewLinkCode makes the one-time code the user sends to the bot. It replaces
// the code the user had. Only its hash is stored.
func NewLinkCode(db *gorm.DB, orgID, userID string) (code string, expiresAt time.Time, err error) {
	raw := make([]byte, linkCodeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("failed generating the link code, err=%w", err)
	}
	for i, b := range raw {
		// 256 is a multiple of the alphabet size, so every letter is as likely.
		raw[i] = linkCodeAlphabet[int(b)%len(linkCodeAlphabet)]
	}
	code = string(raw)
	expiresAt = time.Now().UTC().Add(linkCodeTTL)
	if err := models.SetSlackLinkCode(db, orgID, userID, hashLinkCode(code), expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("failed storing the link code, err=%w", err)
	}
	return code, expiresAt, nil
}

// hashLinkCode ignores case, spaces and dashes: a person types the code.
func hashLinkCode(code string) string {
	code = strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(code))
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// linkCommand answers `/hoop link <code>`. ok is false when the command is
// not a link command, or the org has the flag off: the caller then answers as
// it does any other command.
//
// Slack names the user who sent the command, so the code proves the Hoop
// user and Slack proves the Slack user. Nobody types a Slack ID.
func (s *SlackService) linkCommand(cmd slack.SlashCommand) (reply string, ok bool) {
	fields := strings.Fields(cmd.Text)
	if len(fields) == 0 || !strings.EqualFold(fields[0], LinkCommandName) {
		return "", false
	}
	if !featureflag.IsEnabled(s.instanceID, featureflag.FlagSlackConnectCode) {
		return "", false
	}
	if len(fields) < 2 {
		return linkNoCodeMsg, true
	}
	if !s.inWorkspace(cmd.TeamID, cmd.EnterpriseID) {
		log.With("org", s.instanceID).Infof("refused the link command of slack user %s: team %s is not the bot's", cmd.UserID, cmd.TeamID)
		return linkOtherWorkspaceMsg, true
	}
	user, err := models.RedeemSlackLinkCode(models.DB, s.instanceID, hashLinkCode(strings.Join(fields[1:], "")), cmd.UserID)
	switch {
	case errors.Is(err, models.ErrNotFound):
		return linkInvalidMsg, true
	case errors.Is(err, models.ErrSlackIDInUse):
		return linkInUseMsg, true
	case err != nil:
		log.With("org", s.instanceID).Errorf("failed linking slack user %s, err=%v", cmd.UserID, err)
		return linkFailedMsg, true
	}
	log.With("org", s.instanceID).Infof("linked slack user %s to %s with a one-time code", cmd.UserID, user.Email)
	liveevents.Publish(s.instanceID, liveevents.Event{Type: liveevents.UserSlackLinked, ID: user.Subject})
	return fmt.Sprintf(linkDoneMsgFormat, user.Email), true
}

// inWorkspace reports whether a command came from the bot's workspace, or
// from another one of the same Enterprise Grid org. Slack also delivers the
// commands that members of other companies send in a shared channel.
func (s *SlackService) inWorkspace(teamID, enterpriseID string) bool {
	if s.teamID == "" || teamID == "" || teamID == s.teamID {
		return true
	}
	return s.enterpriseID != "" && enterpriseID == s.enterpriseID
}
