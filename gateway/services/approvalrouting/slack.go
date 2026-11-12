package approvalrouting

import (
	"context"
	"fmt"

	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

func init() {
	RegisterNotifier(IntegrationSlack, slackNotifier{})
}

// SlackMaxButtons is slackplugin.SlackMaxButtons, the most approval groups
// a review message carries. The plugin imports the review API, which
// imports this package, so it is restated here; apisidecar tests that the
// two agree.
const SlackMaxButtons = 20

// causeNoSlackID is why a user without a Slack id gets no direct message.
const causeNoSlackID = "no slack id"

// slackNotifier posts the review card the slack plugin posts today: in a
// channel, or as a direct message to a user's slack_id.
type slackNotifier struct{}

func (slackNotifier) Integrated(_ context.Context, orgID string) bool {
	return slackservice.GetServiceInstance(orgID) != nil
}

// Channels are the Slack channels of the listener in scope (of every
// listener of the org with a zero scope) and the org default channel. Slack
// is not asked for names: that needs a scope the app may not have, so the
// name is the id.
func (slackNotifier) Channels(ctx context.Context, db *gorm.DB, orgID string, scope ChannelScope) ([]Channel, error) {
	svc := slackservice.GetServiceInstance(orgID)
	if svc == nil {
		return nil, nil
	}
	var ids []string
	if scope.SidecarID != "" {
		listener, err := models.ResolveSidecarSlackChannels(db.WithContext(ctx), orgID, scope.SidecarID, scope.ListenerName)
		if err != nil {
			return nil, err
		}
		ids = append(ids, listener...)
	} else {
		var rows []pq.StringArray
		err := db.WithContext(ctx).Raw(`SELECT channels FROM private.sidecar_slack_channels WHERE org_id = ?`, orgID).
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			ids = append(ids, r...)
		}
	}
	if def := svc.DefaultChannel(); def != "" {
		ids = append(ids, def)
	}
	var out []Channel
	for _, id := range normalizeNames(ids) {
		out = append(out, Channel{ID: id, Name: id})
	}
	return out, nil
}

// UsersReachable reports a user without a slack_id as unreachable.
func (slackNotifier) UsersReachable(ctx context.Context, db *gorm.DB, orgID string, hoopUserIDs []string) (map[string]string, error) {
	ids, err := slackIDs(ctx, db, orgID, hoopUserIDs)
	if err != nil {
		return nil, err
	}
	causes := make(map[string]string, len(hoopUserIDs))
	for _, u := range hoopUserIDs {
		if ids[u] == "" {
			causes[u] = causeNoSlackID
		}
	}
	return causes, nil
}

// Post sends one message per channel and per user's slack_id. A user
// without one is a failure with causeNoSlackID.
func (slackNotifier) Post(ctx context.Context, db *gorm.DB, orgID string, card ReviewCard, t Targets) PostResult {
	out := PostResult{Failed: map[string]string{}}
	fail := func(cause string) PostResult {
		for _, id := range t.ChannelIDs {
			out.Failed[id] = cause
		}
		for _, id := range t.HoopUserIDs {
			out.Failed[id] = cause
		}
		return out
	}
	svc := slackservice.GetServiceInstance(orgID)
	if svc == nil {
		return fail("slack is not integrated")
	}
	// The ceiling both existing senders apply: a message with no buttons
	// is a notification nobody can act on.
	if len(card.Groups) == 0 || len(card.Groups) >= SlackMaxButtons {
		return fail(fmt.Sprintf("a review message cannot carry %d approval groups", len(card.Groups)))
	}
	slackIDOf, err := slackIDs(ctx, db, orgID, t.HoopUserIDs)
	if err != nil {
		return fail("failed reading the slack ids: " + err.Error())
	}

	destinations := append([]string{}, t.ChannelIDs...)
	userOf := map[string]string{}
	for _, u := range t.HoopUserIDs {
		sid := slackIDOf[u]
		if sid == "" {
			out.Failed[u] = causeNoSlackID
			continue
		}
		userOf[sid] = u
		destinations = append(destinations, sid)
	}
	destinations = normalizeNames(destinations)
	if len(destinations) == 0 {
		return out
	}

	req := &slackservice.MessageReviewRequest{
		ID:             card.ReviewID,
		SessionID:      card.SessionID,
		Name:           card.Requester,
		Email:          card.RequesterEmail,
		UserGroups:     card.RequesterGroups,
		Connection:     card.Connection,
		ConnectionType: card.ConnectionType,
		ApprovalGroups: card.Groups,
		Script:         card.Statement,
		WebappURL:      card.ApprovalURL,
		ExpiresAt:      card.ExpiresAt,
		SlackChannels:  destinations,
		// Only the selected destinations: never the default channel too.
		DefaultChannelAsFallback: true,
	}
	res := svc.PostMessageReview(req)
	out.Posted = res.Posted
	for dest, cause := range res.Failed {
		if u, ok := userOf[dest]; ok {
			out.Failed[u] = cause
			continue
		}
		out.Failed[dest] = cause
	}
	return out
}

// slackIDs maps users that may settle a review (models.ApproverStatuses)
// onto their slack_id ("" without one).
func slackIDs(ctx context.Context, db *gorm.DB, orgID string, userIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ID      string  `gorm:"column:id"`
		SlackID *string `gorm:"column:slack_id"`
	}
	err := db.WithContext(ctx).Raw(`SELECT id, slack_id FROM private.users WHERE org_id = ? AND id IN ? AND status IN ?`,
		orgID, userIDs, models.ApproverStatuses).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.SlackID != nil {
			out[r.ID] = *r.SlackID
		}
	}
	return out, nil
}
