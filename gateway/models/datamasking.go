package models

import (
	"encoding/json"
	"fmt"
	"time"

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
	ConnectionIDs        pq.StringArray           `gorm:"column:connection_ids;type:text[];->"`
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
			`, rule.OrgID, rule.ID, connID).
				Error
			if err == gorm.ErrForeignKeyViolated {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func UpdateDataMaskingRule(rule *DataMaskingRule) (*DataMaskingRule, error) {
	return rule, DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("private.datamasking_rules").
			Where("org_id = ? AND id = ?", rule.OrgID, rule.ID).
			Select("description", "supported_entity_types", "custom_entity_types", "updated_at").
			Updates(DataMaskingRule{
				Description:          rule.Description,
				SupportedEntityTypes: rule.SupportedEntityTypes,
				CustomEntityTypes:    rule.CustomEntityTypes,
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

func ListDataMaskingRules(orgID string) ([]DataMaskingRule, error) {
	var rules []DataMaskingRule
	return rules, DB.Raw(`
	SELECT
		r.id, r.org_id, r.name, r.description, r.supported_entity_types, r.custom_entity_types,
		(
			SELECT ARRAY_AGG(connection_id) FROM private.datamasking_rules_connections
			WHERE org_id = ? AND rule_id = r.id AND status = 'active'
		) AS connection_ids, r.updated_at
	FROM private.datamasking_rules r
	WHERE org_id = ?
	`, orgID, orgID).
		Find(&rules).
		Error
}

func GetDataMaskingRuleByID(orgID, ruleID string) (*DataMaskingRule, error) {
	var rule DataMaskingRule
	err := DB.Raw(`
	SELECT
		r.id, r.org_id, r.name, r.description, r.supported_entity_types, r.custom_entity_types,
		(
			SELECT ARRAY_AGG(connection_id) FROM private.datamasking_rules_connections
			WHERE org_id = ? AND rule_id = r.id AND status = 'active'
		) AS connection_ids, r.updated_at
	FROM private.datamasking_rules r
	WHERE org_id = ? AND r.id = ?
	`, orgID, orgID, ruleID).
		First(&rule).
		Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	return &rule, err
}

func GetDataMaskingEntityTypes(orgID, connID string) (json.RawMessage, error) {
	var jsonStr string
	err := DB.Raw(`
		SELECT COALESCE(json_agg(
			json_build_object(
				'id', r.id,
				'name', r.name,
				'supported_entity_types', r.supported_entity_types,
				'custom_entity_types', r.custom_entity_types
			)), '[]'::json)
		FROM private.datamasking_rules r
		INNER JOIN private.datamasking_rules_connections c ON r.id = c.rule_id
		WHERE c.org_id = ? AND c.connection_id = ? AND c.status = 'active'`, orgID, connID).
		Row().
		Scan(&jsonStr)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(jsonStr), nil
}

func MigratePluginConnectionToDataMaskingRules(orgID string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Exec(`
		INSERT INTO private.datamasking_rules (id, org_id, name, description, supported_entity_types, created_at, updated_at)
		SELECT
			pc.id,
			pc.org_id,
			'dm_' || c.name AS name,  -- Make unique names per connection
			'Migrated from DLP plugin' AS description,
			json_build_array(
				json_build_object(
					'name', 'CUSTOM_SELECTION',
					'entity_types', array_to_json(pc.config)
				)
			) AS supported_entity_types,
			pc.created_at,
			pc.updated_at
		FROM private.plugin_connections pc
		INNER JOIN private.connections c ON pc.connection_id = c.id
		INNER JOIN private.plugins p ON pc.plugin_id = p.id
		WHERE p.name = 'dlp' AND pc.enabled = TRUE AND p.org_id = ?
		ON CONFLICT DO NOTHING`, orgID).
			Error
		if err != nil {
			return fmt.Errorf("failed migrating plugin connections to data masking rules: %v", err)
		}
		return tx.Exec(`
		INSERT INTO private.datamasking_rules_connections (org_id, rule_id, connection_id, status, created_at)
		SELECT
			pc.org_id,
			pc.id AS rule_id,
			pc.connection_id,
			CASE
				WHEN pc.enabled = TRUE THEN 'active'::private.enum_datamasking_assoc_status
				ELSE 'inactive'::private.enum_datamasking_assoc_status
			END AS status,
			pc.created_at
		FROM private.plugin_connections pc
		INNER JOIN private.connections c ON pc.connection_id = c.id
		INNER JOIN private.plugins p ON pc.plugin_id = p.id
		WHERE p.name = 'dlp' AND pc.enabled = TRUE AND p.org_id = ?
		ON CONFLICT DO NOTHING`, orgID).Error
	})
}

func DeleteDataMaskingRule(orgID, ruleID string) error {
	return DB.Table("private.datamasking_rules").
		Where("org_id = ? AND id = ?", orgID, ruleID).
		Delete(&DataMaskingRule{}).
		Error
}
