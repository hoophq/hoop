package sessionapi

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
)

type AIReviewRequester struct {
	UserID       string
	UserEmail    string
	UserName     string
	UserSlackID  string
	ConnectionID string
}

func CreateReviewFromAIAnalysis(
	orgID uuid.UUID,
	sessionID string,
	connectionName string,
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
		ConnectionName:        connectionName,
		ConnectionID:          sql.NullString{String: requester.ConnectionID, Valid: requester.ConnectionID != ""},
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
	return rev, nil
}
