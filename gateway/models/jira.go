package models

import (
	"errors"
	"time"

	"github.com/hoophq/hoop/common/log"
	"gorm.io/gorm"
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

func GetJiraIntegration(orgID string) (*JiraIntegration, error) {
	log.Debugf("getting jira integration for org=%s", orgID)

	var jiraIntegration *JiraIntegration
	if err := DB.Where("org_id = ?", orgID).First(&jiraIntegration).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Debugf("jira integration with org_id=%s not found", orgID)
			return nil, nil
		}
		log.Errorf("failed to get jira integration, reason=%v", err)
		return nil, err
	}

	return jiraIntegration, nil
}

func CreateJiraIntegration(orgID string, jiraIntegration *JiraIntegration) (*JiraIntegration, error) {
	log.Debugf("creating jira integration for org=%s", orgID)
	if err := DB.Create(&jiraIntegration).Error; err != nil {
		log.Errorf("failed to create jira integration, reason=%v", err)
		return nil, err
	}

	return jiraIntegration, nil
}

func UpdateJiraIntegration(orgID string, jiraIntegration *JiraIntegration) (*JiraIntegration, error) {
	log.Debugf("updating jira integration for org=%s", orgID)

	var existingIntegration JiraIntegration
	if err := DB.Where("org_id = ?", orgID).First(&existingIntegration).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			log.Errorf("jira integration with org_id=%s not found", orgID)
			return nil, err
		}
		log.Errorf("failed to check if jira integration exists, reason=%v", err)
		return nil, err
	}

	jiraIntegration.UpdatedAt = time.Now()
	if err := DB.Model(&existingIntegration).Where("org_id = ?", orgID).Updates(jiraIntegration).Error; err != nil {
		log.Errorf("failed to update jira integration, reason=%v", err)
		return nil, err
	}

	return jiraIntegration, nil
}
