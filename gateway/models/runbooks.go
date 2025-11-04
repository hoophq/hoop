package models

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Runbooks struct {
	ID                string                       `gorm:"column:id"`
	OrgID             string                       `gorm:"column:org_id"`
	RepositoryConfigs map[string]map[string]string `gorm:"column:repository_configs;serializer:json"`
	CreatedAt         time.Time                    `gorm:"column:created_at"`
	UpdatedAt         time.Time                    `gorm:"column:updated_at"`
}

type RunbookRuleFile struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
}

type RunbookRuleFiles []RunbookRuleFile

func (r RunbookRuleFiles) Value() (driver.Value, error) {
	return json.Marshal(r)
}
func (r *RunbookRuleFiles) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to scan RunbookRuleFiles")
	}
	return json.Unmarshal(bytes, r)
}

type RunbookRules struct {
	ID          string           `gorm:"column:id"`
	OrgID       string           `gorm:"column:org_id"`
	Name        string           `gorm:"column:name"`
	Description sql.NullString   `gorm:"column:description"`
	UserGroups  pq.StringArray   `gorm:"column:user_groups;type:text[]"`
	Connections pq.StringArray   `gorm:"column:connections;type:text[]"`
	Runbooks    RunbookRuleFiles `gorm:"column:runbooks;type:jsonb;serializer:json"`
	CreatedAt   time.Time        `gorm:"column:created_at"`
	UpdatedAt   time.Time        `gorm:"column:updated_at"`
}

func IsUserAllowedToRunRunbook(orgId, connection, runbookRepository, runbookName string, userGroups []string) (bool, error) {
	if slices.Contains(userGroups, types.GroupAdmin) {
		return true, nil
	}

	existsRules := false
	err := DB.Table("private.runbook_rules").
		Select("1").
		Where("org_id = ?", orgId).
		Limit(1).
		Find(&existsRules).Error
	if err != nil {
		return false, err
	}

	if !existsRules {
		return true, nil
	}

	var count int64
	err = DB.
		Table("private.runbook_rules").
		Where(`
		org_id = ? AND
		(CARDINALITY(user_groups) = 0 OR user_groups && ?) AND
		(CARDINALITY(connections) = 0 OR connections && ?) AND
		(JSONB_ARRAY_LENGTH(runbooks) = 0 OR EXISTS (
			SELECT 1
			FROM JSONB_ARRAY_ELEMENTS(runbooks) file
			WHERE file ->> 'repository' = ? AND file ->> 'name' = ?
		))
		`, orgId, pq.StringArray(userGroups), pq.StringArray([]string{connection}), runbookRepository, runbookName).
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func GetRunbookRules(db *gorm.DB, orgID string, offset int, limit int) ([]RunbookRules, error) {
	query := db.Table("private.runbook_rules").Where("org_id = ?", orgID)
	if limit > 0 {
		query = query.Offset(offset).Limit(limit)
	}

	var runbookRules []RunbookRules
	err := query.Find(&runbookRules).Error
	if err != nil {
		return nil, err
	}
	return runbookRules, nil
}

func GetRunbookConfigurationByOrgID(db *gorm.DB, orgID string) (*Runbooks, error) {
	var runbooks Runbooks
	err := db.Table("private.runbooks").Where("org_id = ?", orgID).First(&runbooks).Error
	if err != nil {
		return nil, err
	}
	return &runbooks, nil
}

func UpsertRunbookConfiguration(db *gorm.DB, runbooks *Runbooks) error {
	tx := db.Table("private.runbooks").Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "org_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"repository_configs": runbooks.RepositoryConfigs,
			"updated_at":         time.Now().UTC(),
		}),
	}, clause.Returning{}).Create(runbooks)

	return tx.Error
}

func GetRunbookRuleByID(db *gorm.DB, orgID, ruleID string) (*RunbookRules, error) {
	var rule RunbookRules
	err := db.Table("private.runbook_rules").Where("org_id = ? AND id = ?", orgID, ruleID).First(&rule).Error
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func UpsertRunbookRule(db *gorm.DB, rule *RunbookRules) error {
	return db.Table("private.runbook_rules").
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"name":        rule.Name,
				"description": rule.Description,
				"user_groups": rule.UserGroups,
				"connections": rule.Connections,
				"runbooks":    rule.Runbooks,
				"updated_at":  time.Now().UTC(),
			}),
		}).
		Create(rule).
		Error
}

func DeleteRunbookRule(db *gorm.DB, orgID, ruleID string) error {
	return db.Table("private.runbook_rules").
		Where("id = ? AND org_id = ?", ruleID, orgID).
		Delete(&RunbookRules{}).
		Error
}
