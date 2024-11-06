package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tableGuardRails = "private.guardrail_rules"

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
	return DB.Table(tableGuardRails).
		Where(`org_id = ? and id = ?`, orgID, ruleID).
		Delete(&GuardRailRules{}).Error
}
