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
	return datamaskingJunction.listRulesForSidecar(db, orgID, sidecarID)
}

func SetDataMaskingRuleListeners(db *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return SetDataMaskingRuleListenersTx(tx, orgID, ruleName, targets)
	})
}

func SetDataMaskingRuleListenersTx(tx *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return datamaskingJunction.setTargetsTx(tx, orgID, ruleName, targets)
}

func ListDataMaskingRuleTargets(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]SidecarRuleTarget, error) {
	return datamaskingJunction.ruleTargets(db, orgID, ruleName)
}

func SidecarsBoundToDataMaskingRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	return datamaskingJunction.sidecarsBoundToRule(db, orgID, ruleName)
}
