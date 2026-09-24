package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// The data masking junction, and the calls that bind it. One file per junction
// so a schema change for one binding stays local; the shared types, and why
// the three tables are separate, are in sidecar_rule_bindings.go.

type DatamaskingRuleListener struct {
	OrgID        uuid.UUID `gorm:"column:org_id;primaryKey"`
	RuleName     string    `gorm:"column:datamasking_rule_name;primaryKey"`
	SidecarID    string    `gorm:"column:sidecar_id;primaryKey"`
	ListenerName string    `gorm:"column:listener_name;primaryKey"`
	Position     int       `gorm:"column:position"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (DatamaskingRuleListener) TableName() string { return "private.datamasking_rules_listeners" }

func ListDataMaskingRulesForSidecar(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]BoundRule, error) {
	var out []BoundRule
	err := db.Raw(`
	SELECT b.listener_name, r.name AS rule_name, r.sidecar_spec
	FROM private.datamasking_rules_listeners b
	JOIN private.datamasking_rules r ON r.org_id = b.org_id AND r.name = b.datamasking_rule_name
	WHERE b.org_id = ? AND b.sidecar_id = ?
	ORDER BY b.listener_name, b.position, r.name`, orgID, sidecarID).Scan(&out).Error
	return out, err
}

func SetDataMaskingRuleListeners(db *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return SetDataMaskingRuleListenersTx(tx, orgID, ruleName, targets)
	})
}

func SetDataMaskingRuleListenersTx(tx *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return setRuleListenersTx(tx, "private.datamasking_rules_listeners", "datamasking_rule_name", orgID, ruleName, targets)
}

func ListDataMaskingRuleTargets(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]SidecarRuleTarget, error) {
	var out []SidecarRuleTarget
	err := db.Raw(`
	SELECT sidecar_id, listener_name FROM private.datamasking_rules_listeners
	WHERE org_id = ? AND datamasking_rule_name = ?
	ORDER BY sidecar_id, listener_name`, orgID, ruleName).Scan(&out).Error
	return out, err
}

func SidecarsBoundToDataMaskingRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	var out []string
	err := db.Raw(`SELECT DISTINCT sidecar_id FROM private.datamasking_rules_listeners
	WHERE org_id = ? AND datamasking_rule_name = ?`, orgID, ruleName).Scan(&out).Error
	return out, err
}
