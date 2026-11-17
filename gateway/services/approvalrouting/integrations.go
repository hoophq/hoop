package approvalrouting

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// IntegrationState is one integration as the routing form shows it.
type IntegrationState struct {
	Integrated bool
	Channels   []Channel
}

// ListIntegrations reports the Slack and Teams state of the org and the
// channels each reaches. An integration with no registered notifier is not
// integrated.
func ListIntegrations(ctx context.Context, db *gorm.DB, orgID string) (map[string]IntegrationState, error) {
	out := map[string]IntegrationState{}
	for _, name := range []string{IntegrationSlack, IntegrationTeams} {
		state := IntegrationState{Channels: []Channel{}}
		n := registered(name)
		if n != nil && n.Integrated(ctx, orgID) {
			state.Integrated = true
			channels, err := n.Channels(ctx, db, orgID, ChannelScope{})
			if err != nil {
				return nil, fmt.Errorf("failed listing the %s channels: %w", name, err)
			}
			if channels != nil {
				state.Channels = channels
			}
		}
		out[name] = state
	}
	return out, nil
}
