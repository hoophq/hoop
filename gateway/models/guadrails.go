package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tableGuardRails = "private.guardrail_rules"
const tableGuardRailsConnections = "private.guardrail_rules_connections"

type GuardRailRules struct {
	OrgID       string         `gorm:"column:org_id"`
	ID          string         `gorm:"column:id"`
	Name        string         `gorm:"column:name"`
	Description string         `gorm:"column:description"`
	Input       map[string]any `gorm:"column:input;serializer:json"`
	Output      map[string]any `gorm:"column:output;serializer:json"`
	CreatedAt   time.Time      `gorm:"column:created_at"`
	UpdatedAt   time.Time      `gorm:"column:updated_at"`
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
	return rules,
		DB.Table(tableGuardRails).
			Where("org_id = ?", orgID).Order("name DESC").Find(&rules).Error
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
	return &rule, nil
}

// GetConnectionIDsForGuardRail returns all connection IDs associated with a guardrail
func GetConnectionIDsForGuardRail(orgID, ruleID string) ([]string, error) {

	// Return connection IDs as expected by the function name and API contract
	var connectionIDs []string
	err := DB.Raw(`
		SELECT c.id 
		FROM private.connections c
		JOIN private.guardrail_rules_connections grc ON grc.connection_id = c.id
		WHERE grc.org_id = ? AND grc.rule_id = ?
	`, orgID, ruleID).Pluck("id", &connectionIDs).Error

	if err != nil {
		log.Errorf("Error retrieving connection IDs for guardrail %s: %v", ruleID, err)
		return nil, err
	}

	return connectionIDs, nil
}

// SyncGuardRailConnections updates the connections associated with a guardrail
func SyncGuardRailConnections(orgID, ruleID string, connectionIDs []string) error {
	if len(connectionIDs) == 0 {
		return nil // Nothing to do
	}

	// Start a transaction
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Delete existing connections
	if err := tx.Exec("DELETE FROM "+tableGuardRailsConnections+" WHERE org_id = ? AND rule_id = ?",
		orgID, ruleID).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Keep track of successful associations
	var successCount int

	// Try to find connections by ID or name and add associations
	for _, connNameOrID := range connectionIDs {
		// Try to get the connection directly if it's a UUID
		var conn *Connection
		var err error

		// Try to find the connection
		conn, err = GetConnectionByNameOrID(orgID, connNameOrID)
		if err != nil || conn == nil {
			log.Warnf("Connection not found by ID/name '%s': %v", connNameOrID, err)
			continue
		}

		// Add the association
		newID := uuid.NewString()
		result := tx.Exec(`
			INSERT INTO `+tableGuardRailsConnections+` (id, org_id, rule_id, connection_id, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, newID, orgID, ruleID, conn.ID, time.Now().UTC())

		if result.Error != nil {
			log.Errorf("Error associating connection %s with guardrail: %v", conn.Name, result.Error)
			continue
		}

		if result.RowsAffected == 0 {
			log.Warnf("No rows affected when associating connection %s with guardrail", conn.Name)
			continue
		}

		successCount++
	}

	// If no connections were added but some were specified, return an error
	if successCount == 0 && len(connectionIDs) > 0 {
		log.Errorf("Failed to add any connections to guardrail %s", ruleID)
		tx.Rollback()
		return fmt.Errorf("could not add any connections to guardrail")
	}

	return tx.Commit().Error
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
