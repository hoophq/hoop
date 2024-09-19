package pgrest

import (
	"fmt"
	"time"

	"github.com/hoophq/hoop/gateway/pgrest"
)

type JiraIntegrationStatus string

const (
	JiraIntegrationStatusActive   JiraIntegrationStatus = "enabled"
	JiraIntegrationStatusInactive JiraIntegrationStatus = "disabled"
)

type JiraIntegration struct {
	ID             string                `json:"id"`
	OrgID          string                `json:"org_id"`
	JiraURL        string                `json:"jira_url"`
	JiraUser       string                `json:"jira_user"`
	JiraAPIToken   string                `json:"jira_api_token"`
	JiraProjectKey string                `json:"jira_project_key"`
	Status         JiraIntegrationStatus `json:"status"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

type JiraIntegrations struct{}

func NewJiraIntegrations() *JiraIntegrations {
	return &JiraIntegrations{}
}

// GetJiraIntegration obtém a integração Jira pelo org_id
func (j *JiraIntegrations) GetJiraIntegration(ctx pgrest.OrgContext) (*JiraIntegration, error) {
	var integration JiraIntegration
	err := pgrest.New("/jira_integration?org_id=eq.%s", ctx.GetOrgID()).
		FetchOne().
		DecodeInto(&integration)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &integration, nil
}

// UpdateJiraIntegration atualiza a integração Jira pelo org_id
func (j *JiraIntegrations) UpdateJiraIntegration(ctx pgrest.OrgContext, integration JiraIntegration) (*JiraIntegration, error) {
	var existingIntegration JiraIntegration
	err := pgrest.New("/jira_integration?org_id=eq.%s", ctx.GetOrgID()).
		FetchOne().
		DecodeInto(&existingIntegration)
	if err != nil {
		return nil, err
	}

	var updatedIntegrations []JiraIntegration
	err = pgrest.New("/jira_integration?id=eq.%s", existingIntegration.ID).
		Patch(map[string]interface{}{
			"jira_url":         integration.JiraURL,
			"jira_user":        integration.JiraUser,
			"jira_api_token":   integration.JiraAPIToken,
			"jira_project_key": integration.JiraProjectKey,
			"status":           integration.Status,
			"updated_at":       time.Now(),
		}).
		DecodeInto(&updatedIntegrations)

	if err != nil {
		return nil, err
	}

	if len(updatedIntegrations) == 0 {
		return nil, fmt.Errorf("failed to update Jira integration")
	}

	return &updatedIntegrations[0], nil
}

// CreateJiraIntegration cria uma nova integração Jira
func (j *JiraIntegrations) CreateJiraIntegration(ctx pgrest.OrgContext, integration JiraIntegration) (*JiraIntegration, error) {
	var createdIntegration JiraIntegration
	err := pgrest.New("/jira_integration").
		Create(map[string]interface{}{
			"org_id":           ctx.GetOrgID(),
			"jira_url":         integration.JiraURL,
			"jira_user":        integration.JiraUser,
			"jira_api_token":   integration.JiraAPIToken,
			"jira_project_key": integration.JiraProjectKey,
			"status":           integration.Status,
		}).
		DecodeInto(&createdIntegration)

	if err != nil {
		return nil, err
	}

	return &createdIntegration, nil
}
