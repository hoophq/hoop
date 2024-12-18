package models

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type JiraIntegrationStatus string

const (
	JiraIntegrationStatusActive   JiraIntegrationStatus = "enabled"
	JiraIntegrationStatusInactive JiraIntegrationStatus = "disabled"
)

type JiraIntegration struct {
	ID        string                `json:"id"`
	OrgID     string                `json:"org_id"`
	URL       string                `json:"url"`
	User      string                `json:"user"`
	APIToken  string                `json:"api_token"`
	Status    JiraIntegrationStatus `json:"status"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

func (j JiraIntegration) IsActive() bool { return j.Status == JiraIntegrationStatusActive }
func GetJiraIntegration(orgID string) (*JiraIntegration, error) {
	var jiraIntegration *JiraIntegration
	if err := DB.Where("org_id = ?", orgID).First(&jiraIntegration).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get jira integration, reason=%v", err)
	}

	return jiraIntegration, nil
}

func CreateJiraIntegration(orgID string, jiraIntegration *JiraIntegration) (*JiraIntegration, error) {
	if err := DB.Create(&jiraIntegration).Error; err != nil {
		return nil, fmt.Errorf("failed to create jira integration, reason=%v", err)
	}

	return jiraIntegration, nil
}

func UpdateJiraIntegration(orgID string, newObj *JiraIntegration) (*JiraIntegration, error) {
	var existingIntegration JiraIntegration
	if err := DB.Where("org_id = ?", orgID).First(&existingIntegration).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("jira integration with org_id=%s not found", orgID)
		}
		return nil, fmt.Errorf("failed to check if jira integration exists, reason=%v", err)
	}

	existingIntegration.APIToken = newObj.APIToken
	existingIntegration.Status = newObj.Status
	existingIntegration.URL = newObj.URL
	existingIntegration.User = newObj.User
	if err := DB.Model(&existingIntegration).Where("org_id = ?", orgID).Updates(newObj).Error; err != nil {
		return nil, fmt.Errorf("failed to update jira integration, reason=%v", err)
	}

	return &existingIntegration, nil
}
