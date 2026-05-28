package models

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type SupportedEntityTypesList []SupportedEntityTypesEntry
type CustomEntityTypesList []CustomEntityTypesEntry

type DataMaskingRule struct {
	ID                   string                   `gorm:"column:id"`
	OrgID                string                   `gorm:"column:org_id"`
	Name                 string                   `gorm:"column:name"`
	Description          string                   `gorm:"column:description"`
	SupportedEntityTypes SupportedEntityTypesList `gorm:"column:supported_entity_types;serializer:json"`
	CustomEntityTypes    CustomEntityTypesList    `gorm:"column:custom_entity_types;serializer:json"`
	ScoreThreshold       *float64                 `gorm:"column:score_threshold"`
	RulepackID           sql.NullString           `gorm:"column:rulepack_id"`
	ConnectionIDs        pq.StringArray           `gorm:"column:connection_ids;type:text[];->"`
	Attributes           pq.StringArray           `gorm:"column:attributes;type:text[];->"`
	UpdatedAt            time.Time                `gorm:"column:updated_at"`
}

type SupportedEntityTypesEntry struct {
	Name        string   `json:"name"`
	EntityTypes []string `json:"entity_types"`
}

type CustomEntityTypesEntry struct {
	Name     string   `json:"name"`
	Regex    string   `json:"regex"`
	DenyList []string `json:"deny_list"`
	Score    float64  `json:"score"`
}

type DataMaskingRuleConnection struct {
	ID           string `gorm:"column:id"`
	OrgID        string `gorm:"column:org_id"`
	RuleID       string `gorm:"column:rule_id"`
	ConnectionID string `gorm:"column:connection_id"`
	Status       string `gorm:"column:status"`
}

func (r *SupportedEntityTypesList) Scan(value any) error {
	if value == nil {
		return nil
	}

	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("unsupported data type: %T", value)
	}
	return json.Unmarshal(data, r)
}

func CreateDataMaskingRule(rule *DataMaskingRule) (*DataMaskingRule, error) {
	return rule, DB.Transaction(func(tx *gorm.DB) error {
		return CreateDataMaskingRuleTx(tx, rule)
	})
}

// CreateDataMaskingRuleTx is the transaction-aware variant of CreateDataMaskingRule.
// It runs inside the caller's transaction so the rule (and its connection junction rows)
// can be composed atomically with other writes.
func CreateDataMaskingRuleTx(tx *gorm.DB, rule *DataMaskingRule) error {
	if err := tx.Table("private.datamasking_rules").Create(rule).Error; err != nil {
		if err == gorm.ErrDuplicatedKey {
			return ErrAlreadyExists
		}
		return err
	}
	for _, connID := range rule.ConnectionIDs {
		err := tx.Exec(`
			INSERT INTO private.datamasking_rules_connections (org_id, rule_id, connection_id)
			VALUES (?, ?, ?)
		`, rule.OrgID, rule.ID, connID).Error
		if err == gorm.ErrForeignKeyViolated {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteDataMaskingRulesByRulepackIDTx removes all datamasking rules attached to a
// rulepack within the caller's transaction. Junction tables (rule-attribute,
// rule-connection) cascade automatically via their own FK constraints.
func DeleteDataMaskingRulesByRulepackIDTx(tx *gorm.DB, orgID, rulepackID uuid.UUID) error {
	return tx.Exec(
		`DELETE FROM private.datamasking_rules WHERE org_id = ? AND rulepack_id = ?`,
		orgID, rulepackID,
	).Error
}

func UpdateDataMaskingRule(rule *DataMaskingRule) (*DataMaskingRule, error) {
	return rule, DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("private.datamasking_rules").
			Where("org_id = ? AND id = ?", rule.OrgID, rule.ID).
			Select("description", "supported_entity_types", "custom_entity_types", "score_threshold", "rulepack_id", "updated_at").
			Updates(DataMaskingRule{
				Description:          rule.Description,
				SupportedEntityTypes: rule.SupportedEntityTypes,
				CustomEntityTypes:    rule.CustomEntityTypes,
				ScoreThreshold:       rule.ScoreThreshold,
				RulepackID:           rule.RulepackID,
				UpdatedAt:            rule.UpdatedAt,
			})
		if res.Error != nil {
			return fmt.Errorf("failed updating data masking rule: %v", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}

		err := tx.Table("private.datamasking_rules_connections").
			Where("org_id = ? AND rule_id = ?", rule.OrgID, rule.ID).
			Delete(&DataMaskingRule{}).
			Error
		if err != nil {
			return fmt.Errorf("failed removing data masking associations: %v", err)
		}

		for _, connID := range rule.ConnectionIDs {
			err := tx.Exec(`
			INSERT INTO private.datamasking_rules_connections (org_id, rule_id, connection_id)
			VALUES (?, ?, ?)
			`, rule.OrgID, rule.ID, connID).
				Error
			if err != nil {
				return fmt.Errorf("failed creating data masking association %s: %v", connID, err)
			}
		}
		return nil
	})
}

type DataMaskingListOption struct {
	IncludeAllRulepackOwned bool
	// RulepackID, when non-nil, restricts the result set to rules whose rulepack_id
	// matches. Setting this implicitly includes rulepack-owned rules even when
	// IncludeAllRulepackOwned is false.
	RulepackID *uuid.UUID
}

func ListDataMaskingRules(orgID string, opts ...DataMaskingListOption) ([]DataMaskingRule, error) {
	var opt DataMaskingListOption
	if len(opts) > 0 {
		opt = opts[0]
	}

	extraClause := ""
	args := []any{orgID, orgID, orgID}
	switch {
	case opt.RulepackID != nil:
		extraClause = ` AND r.rulepack_id = ?`
		args = append(args, *opt.RulepackID)
	case !opt.IncludeAllRulepackOwned:
		extraClause = ` AND r.rulepack_id IS NULL`
	}

	var rules []DataMaskingRule
	return rules, DB.Raw(`
	SELECT
		r.id, r.org_id, r.name, r.description, r.supported_entity_types, r.custom_entity_types, r.score_threshold, r.rulepack_id,
		(
			SELECT ARRAY_AGG(connection_id) FROM private.datamasking_rules_connections
			WHERE org_id = ? AND rule_id = r.id AND status = 'active'
		) AS connection_ids,
		COALESCE((
			SELECT ARRAY_AGG(attribute_name) FROM private.datamasking_rules_attributes
			WHERE org_id = ?::uuid AND datamasking_rule_name = r.name
		), ARRAY[]::TEXT[]) AS attributes,
		r.updated_at
	FROM private.datamasking_rules r
	WHERE org_id = ?
	`+extraClause, args...).
		Find(&rules).
		Error
}

func GetDataMaskingRuleByID(orgID, ruleID string) (*DataMaskingRule, error) {
	var rule DataMaskingRule
	err := DB.Raw(`
	SELECT
		r.id, r.org_id, r.name, r.description, r.supported_entity_types, r.custom_entity_types, r.score_threshold, r.rulepack_id,
		(
			SELECT ARRAY_AGG(connection_id) FROM private.datamasking_rules_connections
			WHERE org_id = ? AND rule_id = r.id AND status = 'active'
		) AS connection_ids,
		COALESCE((
			SELECT ARRAY_AGG(attribute_name) FROM private.datamasking_rules_attributes
			WHERE org_id = ?::uuid AND datamasking_rule_name = r.name
		), ARRAY[]::TEXT[]) AS attributes,
		r.updated_at
	FROM private.datamasking_rules r
	WHERE org_id = ? AND r.id = ?
	`, orgID, orgID, orgID, ruleID).
		First(&rule).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &rule, err
}

func GetDataMaskingEntityTypes(orgID, connName string) (json.RawMessage, error) {
	var jsonStr string
	err := DB.Raw(`
		SELECT COALESCE(json_agg(
			json_build_object(
				'id', r.id,
				'name', r.name,
				'supported_entity_types', r.supported_entity_types,
				'score_threshold', r.score_threshold,
				'custom_entity_types', r.custom_entity_types
			)), '[]'::json)
		FROM private.datamasking_rules r
		INNER JOIN private.datamasking_rules_connections c ON r.id = c.rule_id
		INNER JOIN private.connections conn ON c.connection_id = conn.id
		WHERE conn.org_id = ? AND conn.name = ? AND c.status = 'active'`, orgID, connName).
		// WHERE c.org_id = ? AND c.connection_id = ? AND c.status = 'active'`, orgID, connName).
		Row().
		Scan(&jsonStr)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(jsonStr), nil
}

func GetDataMaskingEntityTypesByConnectionAndAttributes(db *gorm.DB, orgID uuid.UUID, connectionName string, attributeNames []string) (json.RawMessage, error) {
	var jsonStr string
	err := db.Raw(`
		SELECT COALESCE(json_agg(
			json_build_object(
				'id', r.id,
				'name', r.name,
				'supported_entity_types', r.supported_entity_types,
				'score_threshold', r.score_threshold,
				'custom_entity_types', r.custom_entity_types
			)), '[]'::json)
		FROM (
			SELECT DISTINCT r.id, r.name, r.supported_entity_types, r.score_threshold, r.custom_entity_types
			FROM private.datamasking_rules r
			LEFT JOIN private.datamasking_rules_connections c ON r.id = c.rule_id AND c.status = 'active'
			LEFT JOIN private.connections conn ON c.connection_id = conn.id AND conn.org_id = r.org_id
			LEFT JOIN private.datamasking_rules_attributes ra ON ra.org_id = r.org_id AND ra.datamasking_rule_name = r.name
			WHERE r.org_id = ? AND (conn.name = ? OR ra.attribute_name IN (?))
		) r`, orgID, connectionName, attributeNames).
		Row().
		Scan(&jsonStr)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(jsonStr), nil
}

func GetDataMaskingEntityTypesByAttributes(db *gorm.DB, orgID uuid.UUID, attributeNames []string) (json.RawMessage, error) {
	var jsonStr string
	err := db.Raw(`
		SELECT COALESCE(json_agg(
			json_build_object(
				'id', r.id,
				'name', r.name,
				'supported_entity_types', r.supported_entity_types,
				'score_threshold', r.score_threshold,
				'custom_entity_types', r.custom_entity_types
			)), '[]'::json)
		FROM (
			SELECT DISTINCT r.id, r.name, r.supported_entity_types, r.score_threshold, r.custom_entity_types
			FROM private.datamasking_rules r
			INNER JOIN private.datamasking_rules_attributes ra ON ra.org_id = r.org_id AND ra.datamasking_rule_name = r.name
			WHERE r.org_id = ? AND ra.attribute_name IN (?)
		) r`, orgID, attributeNames).
		Row().
		Scan(&jsonStr)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(jsonStr), nil
}

func DeleteDataMaskingRule(orgID, ruleID string) error {
	return DB.Table("private.datamasking_rules").
		Where("org_id = ? AND id = ?", orgID, ruleID).
		Delete(&DataMaskingRule{}).
		Error
}

func UpdateDataMaskingRuleConnection(orgID, connectionID string, items []DataMaskingRuleConnection) ([]DataMaskingRuleConnection, error) {
	return items, DB.Table("private.datamasking_rules_connections").Transaction(func(tx *gorm.DB) error {
		err := tx.Exec(`DELETE FROM private.datamasking_rules_connections WHERE org_id = ? AND connection_id = ?`,
			orgID, connectionID).
			Error
		if err != nil {
			return fmt.Errorf("failed deleting existing data masking rule connections: %v", err)
		}
		if err := tx.Create(&items).Error; err != nil {
			return fmt.Errorf("failed creating data masking rule connection: %v", err)
		}
		return nil
	})
}
