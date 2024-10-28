package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const tableGuardRails = "private.guardrail_rules"

// TODO: list all guardrails from a connection (grpc gateway rules)

// TODO: create guardrail rule association (connection create/update)
// TODO: list all associations in the connection (connection get/list)

// TODO: create / update guarrail rules (api rest) - OK
// TODO: list all guardrails from org (api rest) - OK
// TODO: get single rule (api rest) - OK

type GuardRailRules struct {
	OrgID     string         `gorm:"column:org_id"`
	ID        string         `gorm:"column:id"`
	Name      string         `gorm:"column:name"`
	Input     map[string]any `gorm:"column:input;serializer:json"`
	Output    map[string]any `gorm:"column:output;serializer:json"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at"`
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
	return DB.Table(tableGuardRails).Model(rule).Create(rule).Error
}

func UpsertGuardRailRules(r *GuardRailRules) error {
	res := DB.Table(tableGuardRails).
		Model(r).
		Clauses(clause.Returning{}).
		Updates(GuardRailRules{
			Name:      r.Name,
			Input:     r.Input,
			Output:    r.Output,
			UpdatedAt: r.UpdatedAt,
		}).
		Where("org_id = ? AND id = ?", r.OrgID, r.ID)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}
