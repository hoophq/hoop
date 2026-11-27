package slack

// Refusal is why Slack does not vouch for a user.
type Refusal int

const (
	// Vouched: Slack holds an active member of the bot's workspace or grid.
	Vouched Refusal = iota
	RefusalDeactivated
	RefusalBot
	RefusalGuest
	RefusalOtherWorkspace
)

// IdentityRefusal refuses a deleted user, a bot, a guest, and a user from
// outside the workspace.
//
// botTeamID and botEnterpriseID are the workspace and grid the bot token
// belongs to. A user of another workspace in the same Enterprise Grid org is
// accepted; one from outside it is not.
func (u *SlackUser) IdentityRefusal(botTeamID, botEnterpriseID string) Refusal {
	switch {
	case u.Deleted:
		return RefusalDeactivated
	case u.IsBot:
		return RefusalBot
	case u.IsRestricted || u.IsUltraRestricted:
		return RefusalGuest
	case u.IsStranger:
		return RefusalOtherWorkspace
	case !u.InWorkspace(botTeamID, botEnterpriseID):
		return RefusalOtherWorkspace
	}
	return Vouched
}

// InWorkspace reports whether the user's home workspace is the bot's, or
// another one of the same Enterprise Grid org.
func (u *SlackUser) InWorkspace(botTeamID, botEnterpriseID string) bool {
	if botTeamID == "" || u.TeamID == "" || u.TeamID == botTeamID {
		return true
	}
	return botEnterpriseID != "" && u.EnterpriseID == botEnterpriseID
}
