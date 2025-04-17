package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tableJiraIssueTemplates = "private.jira_issue_templates"

type JiraIssueTemplate struct {
	OrgID                      string         `gorm:"column:org_id"`
	ID                         string         `gorm:"column:id"`
	JiraIntegrationID          string         `gorm:"column:jira_integration_id"`
	Name                       string         `gorm:"column:name"`
	Description                string         `gorm:"column:description"`
	ProjectKey                 string         `gorm:"column:project_key"`
	RequestTypeID              string         `gorm:"column:request_type_id"`
	IssueTransitionNameOnClose string         `gorm:"column:issue_transition_name_on_close"`
	MappingTypes               map[string]any `gorm:"column:mapping_types;serializer:json"`
	PromptTypes                map[string]any `gorm:"column:prompt_types;serializer:json"`
	CmdbTypes                  map[string]any `gorm:"column:cmdb_types;serializer:json"`
	ConnectionIDs              pq.StringArray `gorm:"column:connection_ids;type:text[];->"`
	CreatedAt                  time.Time      `gorm:"column:created_at"`
	UpdatedAt                  time.Time      `gorm:"column:updated_at"`
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
	FieldType   string `json:"field_type"`
}

type CmdbType struct {
	JiraObjectType     string `json:"jira_object_type"`
	JiraObjectSchemaId string `json:"jira_object_schema_id"`
	JiraField          string `json:"jira_field"`
	Required           bool   `json:"required"`
	Description        string `json:"description"`
	Value              string `json:"value"`
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
			FieldType:   fmt.Sprintf("%v", obj["field_type"]),
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
			JiraObjectType:     fmt.Sprintf("%v", obj["jira_object_type"]),
			JiraObjectSchemaId: fmt.Sprintf("%v", obj["jira_object_schema_id"]),
			JiraField:          jiraField,
			Required:           fmt.Sprintf("%v", obj["required"]) == "true",
			Description:        fmt.Sprintf("%v", obj["description"]),
			Value:              fmt.Sprintf("%v", obj["value"]),
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
	sess := &gorm.Session{FullSaveAssociations: true}
	return DB.Session(sess).Transaction(func(tx *gorm.DB) error {
		err := tx.Table(tableJiraIssueTemplates).Create(issue).Error
		if err != nil {
			if err == gorm.ErrDuplicatedKey {
				return ErrAlreadyExists
			}
			return err
		}

		return updateConnectionJiraIssueTemplateID(tx, issue)
	})
}

func UpdateJiraIssueTemplates(issue *JiraIssueTemplate) error {
	sess := &gorm.Session{FullSaveAssociations: true}
	return DB.Session(sess).Transaction(func(tx *gorm.DB) error {
		res := tx.Table(tableJiraIssueTemplates).
			Model(issue).
			Clauses(clause.Returning{}).
			Updates(JiraIssueTemplate{
				Description:                issue.Description,
				ProjectKey:                 issue.ProjectKey,
				RequestTypeID:              issue.RequestTypeID,
				IssueTransitionNameOnClose: issue.IssueTransitionNameOnClose,
				MappingTypes:               issue.MappingTypes,
				PromptTypes:                issue.PromptTypes,
				CmdbTypes:                  issue.CmdbTypes,
				UpdatedAt:                  issue.UpdatedAt,
			}).
			Where("org_id = ? AND id = ?", issue.OrgID, issue.ID)
		if res.Error == nil && res.RowsAffected == 0 {
			return ErrNotFound
		}
		if res.Error != nil {
			return res.Error
		}
		return updateConnectionJiraIssueTemplateID(tx, issue)
	})
}

func GetJiraIssueTemplatesByID(orgID, id string) (*JiraIssueTemplate, *JiraIntegration, error) {
	config, err := GetJiraIntegration(orgID)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to verify jira integration: %v", err)
	}
	var issue JiraIssueTemplate
	err = DB.Raw(`
		SELECT
			i.id, i.org_id, i.jira_integration_id, i.name, i.description, i.project_key, i.request_type_id,
			i.issue_transition_name_on_close, i.mapping_types, i.prompt_types, i.cmdb_types, i.updated_at, i.created_at,
			COALESCE((
				SELECT array_agg(id::TEXT) FROM private.connections
				WHERE private.connections.jira_issue_template_id = i.id
			), ARRAY[]::TEXT[]) AS connection_ids
		FROM private.jira_issue_templates i
		WHERE i.org_id = ? AND i.id = ?
		ORDER BY i.name DESC
	`, orgID, id).
		First(&issue).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, config, ErrNotFound
		}
		return nil, config, err
	}
	return &issue, config, nil
}

func ListJiraIssueTemplates(orgID string) ([]*JiraIssueTemplate, error) {
	var issues []*JiraIssueTemplate
	return issues, DB.Raw(`
		SELECT
			i.id, i.org_id, i.jira_integration_id, i.name, i.description, i.project_key, i.request_type_id,
			i.issue_transition_name_on_close, i.mapping_types, i.prompt_types, i.cmdb_types, i.updated_at, i.created_at,
			COALESCE((
				SELECT array_agg(id::TEXT) FROM private.connections
				WHERE private.connections.jira_issue_template_id = i.id
			), ARRAY[]::TEXT[]) AS connection_ids
		FROM private.jira_issue_templates i
		WHERE i.org_id = ?
		ORDER BY i.name DESC`, orgID).
		Find(&issues).Error
}

func DeleteJiraIssueTemplates(orgID, id string) error {
	if _, _, err := GetJiraIssueTemplatesByID(orgID, id); err != nil {
		return err
	}
	return DB.Table(tableJiraIssueTemplates).
		Where(`org_id = ? and id = ?`, orgID, id).
		Delete(&JiraIssueTemplate{}).Error
}

func updateConnectionJiraIssueTemplateID(tx *gorm.DB, issue *JiraIssueTemplate) error {
	// remove all associations
	err := tx.Exec(`
		UPDATE private.connections SET jira_issue_template_id = NULL
		WHERE org_id = ? AND jira_issue_template_id = ?`, issue.OrgID, issue.ID).
		Error
	if err != nil {
		return fmt.Errorf("failed removing jira issue template association, reason=%v", err)
	}

	var notFoundConnections []string
	for _, connID := range issue.ConnectionIDs {
		res := tx.Exec(`
			UPDATE private.connections SET jira_issue_template_id = ?
			WHERE org_id = ? AND id = ?`, issue.ID, issue.OrgID, connID)

		if res.Error != nil {
			return fmt.Errorf("failed creating jira issue template associations, reason=%v", res.Error)
		}
		if res.RowsAffected == 0 {
			notFoundConnections = append(notFoundConnections, connID)
		}
	}
	if len(notFoundConnections) > 0 {
		return fmt.Errorf("unable to update issue template associations, the following connections were not found: %v",
			notFoundConnections)
	}
	return nil
}
