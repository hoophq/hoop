package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tableGuardRails = "private.guardrail_rules"
const tableGuardRailsConnections = "private.guardrail_rules_connections"

type GuardRailRules struct {
	OrgID         string         `gorm:"column:org_id"`
	ID            string         `gorm:"column:id"`
	Name          string         `gorm:"column:name"`
	Description   string         `gorm:"column:description"`
	Input         map[string]any `gorm:"column:input;serializer:json"`
	Output        map[string]any `gorm:"column:output;serializer:json"`
	CreatedAt     time.Time      `gorm:"column:created_at"`
	UpdatedAt     time.Time      `gorm:"column:updated_at"`
	ConnectionIDs []string       `gorm:"-"` // Not stored in DB, populated from join query
}

type GuardRailConnection struct {
	ID           string    `gorm:"column:id"`
	OrgID        string    `gorm:"column:org_id"`
	RuleID       string    `gorm:"column:rule_id"`
	ConnectionID string    `gorm:"column:connection_id"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func ListGuardRailRules(orgID string) ([]*GuardRailRules, error) {
	var rules []*GuardRailRules
	err := DB.Table(tableGuardRails).
		Where("org_id = ?", orgID).
		Order("name DESC").
		Find(&rules).Error

	if err != nil {
		return nil, err
	}

	// Load connection IDs for all rules in a single query
	var connections []struct {
		RuleID       string
		ConnectionID string
	}

	err = DB.Raw(`
		SELECT grc.rule_id, grc.connection_id
		FROM private.guardrail_rules_connections grc
		WHERE grc.org_id = ? AND grc.rule_id IN (?)
	`, orgID, getGuardrailIDs(rules)).Scan(&connections).Error

	if err != nil {
		return nil, err
	}

	// Map connections to rules
	connectionMap := make(map[string][]string)
	for _, conn := range connections {
		connectionMap[conn.RuleID] = append(connectionMap[conn.RuleID], conn.ConnectionID)
	}

	// Populate ConnectionIDs field
	for _, rule := range rules {
		rule.ConnectionIDs = connectionMap[rule.ID]
	}

	return rules, nil
}

func GetGuardRailRules(orgID, ruleID string) (*GuardRailRules, error) {
	var rule GuardRailRules
	if err := DB.Table(tableGuardRails).Where("org_id = ? AND id = ?", orgID, ruleID).
		First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Load connection IDs for this rule
	var connectionIDs []string
	err := DB.Raw(`
		SELECT grc.connection_id
		FROM private.guardrail_rules_connections grc
		WHERE grc.org_id = ? AND grc.rule_id = ?
	`, orgID, ruleID).Pluck("connection_id", &connectionIDs).Error

	if err != nil {
		return nil, err
	}

	rule.ConnectionIDs = connectionIDs
	return &rule, nil
}

// Helper to extract rule IDs from a slice of rules
func getGuardrailIDs(rules []*GuardRailRules) []string {
	ids := make([]string, len(rules))
	for i, rule := range rules {
		ids[i] = rule.ID
	}
	return ids
}

// SyncGuardRailConnectionAssociations updates the connections associated with a guardrail
func SyncGuardRailConnectionAssociations(orgID, ruleID string, connectionIDs []string) error {
	if len(connectionIDs) == 0 {
		return nil
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		// Delete existing connections
		if err := tx.Exec(`DELETE FROM private.guardrail_rules_connections 
			WHERE org_id = ? AND rule_id = ?`, orgID, ruleID).Error; err != nil {
			return err
		}

		// Add new connections that exist
		for _, connNameOrID := range connectionIDs {
			conn, err := GetConnectionByNameOrID(orgID, connNameOrID)
			if err != nil || conn == nil {
				continue // Skip invalid connections
			}

			// Add the association
			err = tx.Exec(`
				INSERT INTO private.guardrail_rules_connections (id, org_id, rule_id, connection_id, created_at)
				VALUES (?, ?, ?, ?, ?)
			`, uuid.NewString(), orgID, ruleID, conn.ID, time.Now().UTC()).Error

			if err != nil {
				return err
			}
		}

		return nil
	})
}

func CreateGuardRailRules(rule *GuardRailRules) error {
	err := DB.Table(tableGuardRails).Model(rule).Create(rule).Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
	return err
}

func UpdateGuardRailRules(r *GuardRailRules) error {
	res := DB.Table(tableGuardRails).
		Model(r).
		Clauses(clause.Returning{}).
		Updates(GuardRailRules{
			Name:        r.Name,
			Description: r.Description,
			Input:       r.Input,
			Output:      r.Output,
			UpdatedAt:   r.UpdatedAt,
		}).
		Where("org_id = ? AND id = ?", r.OrgID, r.ID)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func DeleteGuardRailRules(orgID, ruleID string) error {
	// TODO: change it to perform it in a single transaction
	if _, err := GetGuardRailRules(orgID, ruleID); err != nil {
		return err
	}

	// The associations will be deleted automatically due to the ON DELETE CASCADE
	return DB.Table(tableGuardRails).
		Where(`org_id = ? and id = ?`, orgID, ruleID).
		Delete(&GuardRailRules{}).Error
}
