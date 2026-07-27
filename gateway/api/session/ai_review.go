package sessionapi

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	slackModel "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/transport/plugins/slack"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

type AIReviewRequester struct {
	UserID      string
	UserEmail   string
	UserName    string
	UserSlackID string
	UserGroups  []string
}

func CreateReviewFromAIAnalysis(
	orgID uuid.UUID,
	sessionID string,
	connection *models.Connection,
	requester AIReviewRequester,
	accessRule *models.AccessRequestRule,
	sessionInput string,
	inputEnvVars map[string]string,
	inputClientArgs []string,
) (*models.Review, error) {
	if accessRule == nil {
		return nil, fmt.Errorf("ai analyzer review: access request rule is required")
	}
	if len(accessRule.ReviewersGroups) == 0 {
		return nil, fmt.Errorf("ai analyzer review: access request rule %q has no reviewers_groups configured", accessRule.Name)
	}

	reviewGroups := make([]models.ReviewGroups, 0, len(accessRule.ReviewersGroups))
	for _, groupName := range accessRule.ReviewersGroups {
		reviewGroups = append(reviewGroups, models.ReviewGroups{
			ID:        uuid.NewString(),
			OrgID:     orgID.String(),
			GroupName: groupName,
			Status:    models.ReviewStatusPending,
		})
	}

	minApprovals := len(reviewGroups)
	if !accessRule.AllGroupsMustApprove && accessRule.MinApprovals != nil {
		minApprovals = *accessRule.MinApprovals
	}

	rev := &models.Review{
		ID:                    uuid.NewString(),
		OrgID:                 orgID.String(),
		Type:                  models.ReviewTypeOneTime,
		SessionID:             sessionID,
		ConnectionName:        connection.Name,
		ConnectionID:          sql.NullString{String: connection.ID, Valid: connection.ID != ""},
		InputEnvVars:          inputEnvVars,
		InputClientArgs:       inputClientArgs,
		OwnerID:               requester.UserID,
		OwnerEmail:            requester.UserEmail,
		OwnerName:             ptr.String(requester.UserName),
		OwnerSlackID:          ptr.String(requester.UserSlackID),
		Status:                models.ReviewStatusPending,
		ReviewGroups:          reviewGroups,
		ForceApprovalGroups:   accessRule.ForceApprovalGroups,
		AccessRequestRuleName: &accessRule.Name,
		MinApprovals:          &minApprovals,
		CreatedAt:             time.Now().UTC(),
	}

	log.With("sid", sessionID, "review-id", rev.ID, "rule", accessRule.Name, "org", orgID).
		Infof("ai analyzer creating onetime review")

	if err := models.CreateReview(rev, sessionInput); err != nil {
		return nil, fmt.Errorf("ai analyzer review: failed creating review: %w", err)
	}

	if err := sendSlackMessage(requester, connection, rev, sessionInput); err != nil {
		log.With("sid", sessionID, "review-id", rev.ID).Errorf("failed sending slack message for ai analyzer review: %v", err)
		// do not return error if slack message sending fails, as the review is already created and actionable in the webapp
	}

	return rev, nil
}

func sendSlackMessage(requester AIReviewRequester, connection *models.Connection, rev *models.Review, reviewInput string) error {
	slackSvc := slack.GetSlackServiceInstance(rev.OrgID)
	log.With("sid", rev.SessionID).Infof("executing slack on-receive, hasinstance=%v", slackSvc != nil)
	if slackSvc == nil {
		return nil
	}

	if rev.Status != models.ReviewStatusPending {
		return nil
	}

	pluginConfig, err := models.GetPluginConnection(connection.OrgID, plugintypes.PluginSlackName, connection.ID)
	if err != nil {
		log.With("sid", rev.SessionID).Errorf("failed fetching plugin connection for slack review message: %v", err)
		return err
	}
	if pluginConfig == nil {
		log.With("sid", rev.SessionID).Infof("no plugin connection found for slack review message, skipping")
		return nil
	}
	if len(pluginConfig.Config) == 0 {
		log.With("sid", rev.SessionID).Infof("plugin connection for slack review message has empty config, skipping")
		return nil
	}

	sreq := &slackModel.MessageReviewRequest{
		Name:           requester.UserName,
		Email:          requester.UserEmail,
		Connection:     rev.ConnectionName,
		ConnectionType: connection.Type,
		SessionID:      rev.SessionID,
		UserGroups:     requester.UserGroups,
		SlackChannels:  pluginConfig.Config,
	}

	appc := appconfig.Get()
	sreq.ID = rev.ID
	sreq.WebappURL = fmt.Sprintf("%s/sessions/%s", appc.ApiURL(), rev.SessionID)
	sreq.ApprovalGroups = slack.ParseGroups(rev.ReviewGroups)
	if rev.AccessDurationSec > 0 {
		ad := time.Duration(rev.AccessDurationSec) * time.Second
		sreq.SessionTime = &ad
	}
	sreq.Script = reviewInput

	if sreq.WebappURL == "" || len(sreq.ApprovalGroups) == 0 || len(sreq.ApprovalGroups) >= slack.SlackMaxButtons {
		log.With("sid", rev.SessionID).Infof("no review message to process, has-webapp-url=%v, approval-groups=%v/%v",
			sreq.WebappURL != "", len(sreq.ApprovalGroups), slack.SlackMaxButtons)
		return nil
	}
	log.With("sid", rev.SessionID).Infof("sending slack review message, conn=%v, jit=%v", sreq.Connection, sreq.SessionTime != nil)
	result := slackSvc.SendMessageReview(sreq)
	log.With("sid", rev.SessionID).Infof("review slack message sent, %v", result)
	return nil
}
