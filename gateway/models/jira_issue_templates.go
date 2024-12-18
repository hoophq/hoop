package models

import (
	"errors"
	"fmt"
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
	CmdbTypes         map[string]any `gorm:"column:cmdb_types;serializer:json"`
	CreatedAt         time.Time      `gorm:"column:created_at"`
	UpdatedAt         time.Time      `gorm:"column:updated_at"`
}

type MappingType struct {
	Description string `json:"description"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	JiraField   string `json:"jira_field"`
}

type PromptType struct {
	Description string `json:"description"`
	Label       string `json:"label"`
	Required    bool   `json:"required"`
	JiraField   string `json:"jira_field"`
}

type CmdbType struct {
	JiraObjectType string `json:"jira_object_type"`
	JiraField      string `json:"jira_field"`
	Required       bool   `json:"required"`
	Description    string `json:"description"`
	Value          string `json:"value"`
}

func (t *JiraIssueTemplate) DecodeMappingTypes() (map[string]MappingType, map[string]PromptType, map[string]CmdbType, error) {
	mappingTypes := map[string]MappingType{}
	items, err := decodeTypesToMapList(t.MappingTypes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("unable to decode mapping_types: %v", err)
	}
	for _, obj := range items {
		jiraField := fmt.Sprintf("%v", obj["jira_field"])
		mappingTypes[jiraField] = MappingType{
			Description: fmt.Sprintf("%v", obj["description"]),
			Type:        fmt.Sprintf("%v", obj["type"]),
			Value:       fmt.Sprintf("%v", obj["value"]),
			JiraField:   jiraField,
		}
	}

	promptTypes := map[string]PromptType{}
	items, err = decodeTypesToMapList(t.PromptTypes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("unable to decode prompt_types: %v", err)
	}
	for _, obj := range items {
		jiraField := fmt.Sprintf("%v", obj["jira_field"])
		promptTypes[jiraField] = PromptType{
			Description: fmt.Sprintf("%v", obj["description"]),
			Label:       fmt.Sprintf("%v", obj["label"]),
			Required:    fmt.Sprintf("%v", obj["required"]) == "true",
			JiraField:   jiraField,
		}
	}

	cmdbTypes := map[string]CmdbType{}
	items, err = decodeTypesToMapList(t.CmdbTypes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("unable to decode cmd_types: %v", err)
	}
	for _, obj := range items {
		jiraField := fmt.Sprintf("%v", obj["jira_field"])
		cmdbTypes[jiraField] = CmdbType{
			JiraObjectType: fmt.Sprintf("%v", obj["jira_object_type"]),
			JiraField:      jiraField,
			Required:       fmt.Sprintf("%v", obj["required"]) == "true",
			Description:    fmt.Sprintf("%v", obj["description"]),
			Value:          fmt.Sprintf("%v", obj["value"]),
		}
	}
	return mappingTypes, promptTypes, cmdbTypes, nil
}

func decodeTypesToMapList(templateTypes map[string]any) ([]map[string]any, error) {
	res := []map[string]any{}
	obj, ok := templateTypes["items"]
	if !ok || obj == nil {
		return res, nil
	}
	items, ok := obj.([]any)
	if !ok {
		return nil, fmt.Errorf(`unable to parse "items" attribute, type=%T`, obj)
	}
	for i, entry := range items {
		data, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unable to parse item (%v), type=%T", i, entry)
		}
		res = append(res, data)
	}
	return res, nil
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
			CmdbTypes:     issue.CmdbTypes,
			UpdatedAt:     issue.UpdatedAt,
		}).
		Where("org_id = ? AND id = ?", issue.OrgID, issue.ID)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func GetJiraIssueTemplatesByID(orgID, id string) (*JiraIssueTemplate, *JiraIntegration, error) {
	config, err := GetJiraIntegration(orgID)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to verify jira integration: %v", err)
	}
	var issue JiraIssueTemplate
	if err := DB.Table(tableJiraIssueTemplates).Where("org_id = ? AND id = ?", orgID, id).
		First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, config, ErrNotFound
		}
		return nil, config, err
	}
	return &issue, config, nil
}

func ListJiraIssueTemplates(orgID string) ([]*JiraIssueTemplate, error) {
	var issues []*JiraIssueTemplate
	return issues,
		DB.Table(tableJiraIssueTemplates).
			Where("org_id = ?", orgID).Order("name DESC").Find(&issues).Error

}

func DeleteJiraIssueTemplates(orgID, id string) error {
	if _, _, err := GetJiraIssueTemplatesByID(orgID, id); err != nil {
		return err
	}
	return DB.Table(tableJiraIssueTemplates).
		Where(`org_id = ? and id = ?`, orgID, id).
		Delete(&JiraIssueTemplate{}).Error
}
