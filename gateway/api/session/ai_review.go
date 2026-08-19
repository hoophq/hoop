package sessionapi

import (
	"database/sql"
	"errors"
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

// ReviewStatementKeys ties a review to one exact statement, for the
// hoop-inspect approval gate. Nil for every other caller, which is every
// review whose unit is a whole session rather than a single statement.
//
// The two are not interchangeable. StatementHash is the AUTHORIZATION key and
// is computed by the relay from the bytes on the wire; RequestMarker is
// caller-supplied and only decides whether an incoming request is a retry of
// one already filed.
type ReviewStatementKeys struct {
	StatementHash string
	RequestMarker string
}

// ErrNoReviewersConfigured means the access rule that would govern a review
// names no groups to review it.
//
// A CONFIG gap rather than a runtime failure: nobody could ever answer the
// review, so filing one would strand it. Callers that persist a session to
// anchor a review should check this BEFORE writing anything, since discovering
// it afterwards leaves an open session with no review attached to it.
var ErrNoReviewersConfigured = errors.New("access request rule has no reviewers_groups configured")

// ValidateAccessRuleForReview reports whether a review can be created against
// this access rule at all.
//
// Exported so a caller can check the same preconditions BEFORE it persists
// anything, and used by CreateReviewFromAIAnalysis itself so the two cannot
// drift: a rule that passes here is one CreateReviewFromAIAnalysis will not
// reject for the same reason.
//
// It validates only what is knowable without touching the database. A caller
// that passes still has to handle a creation failure — this narrows the
// window, it does not remove it.
func ValidateAccessRuleForReview(accessRule *models.AccessRequestRule) error {
	if accessRule == nil {
		return errors.New("access request rule is required")
	}
	if len(accessRule.ReviewersGroups) == 0 {
		return fmt.Errorf("%w: rule %q", ErrNoReviewersConfigured, accessRule.Name)
	}
	return nil
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
	analysis *models.SessionAIAnalysis,
	keys *ReviewStatementKeys,
) (*models.Review, error) {
	if err := ValidateAccessRuleForReview(accessRule); err != nil {
		return nil, fmt.Errorf("ai analyzer review: %w", err)
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
	if keys != nil {
		rev.StatementHash = &keys.StatementHash
		if keys.RequestMarker != "" {
			// Left NULL rather than empty when absent, so the dedupe index
			// holds only rows a marker lookup can actually match: an agent
			// that supplies no marker means every attempt is a new request.
			rev.RequestMarker = &keys.RequestMarker
		}
	}

	log.With("sid", sessionID, "review-id", rev.ID, "rule", accessRule.Name, "org", orgID).
		Infof("ai analyzer creating onetime review")

	if err := models.CreateReview(rev, sessionInput); err != nil {
		return nil, fmt.Errorf("ai analyzer review: failed creating review: %w", err)
	}

	if err := sendSlackMessage(requester, connection, rev, sessionInput, analysis); err != nil {
		log.With("sid", sessionID, "review-id", rev.ID).Errorf("failed sending slack message for ai analyzer review: %v", err)
		// do not return error if slack message sending fails, as the review is already created and actionable in the webapp
	}

	return rev, nil
}

func sendSlackMessage(requester AIReviewRequester, connection *models.Connection, rev *models.Review, reviewInput string, analysis *models.SessionAIAnalysis) error {
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
	if analysis != nil {
		sreq.AIRiskLevel = analysis.RiskLevel
		sreq.AITitle = analysis.Title
		sreq.AISummary = analysis.Summary
		sreq.AIExplanation = analysis.Explanation
	}

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
