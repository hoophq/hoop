package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// The analyzer junction, and the calls that bind it. One file per junction so
// a schema change for one binding stays local; the shared types, and why the
// three tables are separate, are in sidecar_rule_bindings.go.

type AnalyzerRuleListener struct {
	OrgID        uuid.UUID `gorm:"column:org_id;primaryKey"`
	RuleName     string    `gorm:"column:analyzer_rule_name;primaryKey"`
	SidecarID    string    `gorm:"column:sidecar_id;primaryKey"`
	ListenerName string    `gorm:"column:listener_name;primaryKey"`
	Position     int       `gorm:"column:position"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (AnalyzerRuleListener) TableName() string {
	return "private.ai_session_analyzer_rules_listeners"
}

func ListAnalyzerRulesForSidecar(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]BoundRule, error) {
	return analyzerJunction.listRulesForSidecar(db, orgID, sidecarID)
}

func SetAnalyzerRuleListeners(db *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return SetAnalyzerRuleListenersTx(tx, orgID, ruleName, targets)
	})
}

func SetAnalyzerRuleListenersTx(tx *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return analyzerJunction.setTargetsTx(tx, orgID, ruleName, targets)
}

func ListAnalyzerRuleTargets(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]SidecarRuleTarget, error) {
	return analyzerJunction.ruleTargets(db, orgID, ruleName)
}

func SidecarsBoundToAnalyzerRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	return analyzerJunction.sidecarsBoundToRule(db, orgID, ruleName)
}
