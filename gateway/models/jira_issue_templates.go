package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tableJiraIssueTemplates = "private.jira_issue_templates"

type JiraIssueTemplate struct {
	OrgID             string         `gorm:"column:org_id"`
	ID                string         `gorm:"column:id"`
	JiraIntegrationID string         `gorm:"column:jira_integration_id"`
	Name              string         `gorm:"column:name"`
	Description       string         `gorm:"column:description"`
	ProjectKey        string         `gorm:"column:project_key"`
	IssueTypeName     string         `gorm:"column:issue_type_name"`
	MappingTypes      map[string]any `gorm:"column:mapping_types;serializer:json"`
	PromptTypes       map[string]any `gorm:"column:prompt_types;serializer:json"`
	CreatedAt         time.Time      `gorm:"column:created_at"`
	UpdatedAt         time.Time      `gorm:"column:updated_at"`
}

func CreateJiraIssueTemplates(issue *JiraIssueTemplate) error {
	integration, err := GetJiraIntegration(issue.OrgID)
	if err != nil {
		return err
	}
	if integration == nil {
		return ErrNotFound
	}
	issue.JiraIntegrationID = integration.ID
	err = DB.Table(tableJiraIssueTemplates).Create(issue).Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
	return err
}

func UpdateJiraIssueTemplates(issue *JiraIssueTemplate) error {
	res := DB.Table(tableJiraIssueTemplates).
		Model(issue).
		Clauses(clause.Returning{}).
		Updates(JiraIssueTemplate{
			Description:   issue.Description,
			ProjectKey:    issue.ProjectKey,
			IssueTypeName: issue.IssueTypeName,
			MappingTypes:  issue.MappingTypes,
			PromptTypes:   issue.PromptTypes,
			UpdatedAt:     issue.UpdatedAt,
		}).
		Where("org_id = ? AND id = ?", issue.OrgID, issue.ID)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func GetJiraIssueTemplatesByID(orgID, id string) (*JiraIssueTemplate, error) {
	var issue JiraIssueTemplate
	if err := DB.Table(tableJiraIssueTemplates).Where("org_id = ? AND id = ?", orgID, id).
		First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &issue, nil
}

func ListJiraIssueTemplates(orgID string) ([]*JiraIssueTemplate, error) {
	var issues []*JiraIssueTemplate
	return issues,
		DB.Table(tableJiraIssueTemplates).
			Where("org_id = ?", orgID).Order("name DESC").Find(&issues).Error

}

func DeleteJiraIssueTemplates(orgID, id string) error {
	if _, err := GetJiraIssueTemplatesByID(orgID, id); err != nil {
		return err
	}
	return DB.Table(tableJiraIssueTemplates).
		Where(`org_id = ? and id = ?`, orgID, id).
		Delete(&JiraIssueTemplate{}).Error
}
