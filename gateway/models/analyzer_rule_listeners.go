package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The analyzer junction, and the calls that bind it. One file per junction so
// a schema change for one binding stays local; the shared types, and why the
// three tables are separate, are in sidecar_rule_bindings.go.

type AnalyzerRuleListener struct {
	OrgID        uuid.UUID `gorm:"column:org_id;primaryKey"`
	RuleName     string    `gorm:"column:analyzer_rule_name;primaryKey"`
	SidecarID    string    `gorm:"column:sidecar_id;primaryKey"`
	ListenerName string    `gorm:"column:listener_name;primaryKey"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (AnalyzerRuleListener) TableName() string {
	return "private.ai_session_analyzer_rules_listeners"
}

func ListAnalyzerRulesForSidecar(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]BoundRule, error) {
	var out []BoundRule
	err := db.Raw(`
	SELECT b.listener_name, r.name AS rule_name, r.sidecar_spec
	FROM private.ai_session_analyzer_rules_listeners b
	JOIN private.ai_session_analyzer_rules r ON r.org_id = b.org_id AND r.name = b.analyzer_rule_name
	WHERE b.org_id = ? AND b.sidecar_id = ?
	ORDER BY b.listener_name, r.name`, orgID, sidecarID).Scan(&out).Error
	return out, err
}

func SetAnalyzerRuleListeners(db *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return SetAnalyzerRuleListenersTx(tx, orgID, ruleName, targets)
	})
}

func SetAnalyzerRuleListenersTx(tx *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	err := tx.Where("org_id = ? AND analyzer_rule_name = ?", orgID, ruleName).
		Delete(&AnalyzerRuleListener{}).Error
	if err != nil || len(targets) == 0 {
		return err
	}
	rows := make([]AnalyzerRuleListener, 0, len(targets))
	for _, t := range targets {
		rows = append(rows, AnalyzerRuleListener{
			OrgID: orgID, RuleName: ruleName,
			SidecarID: t.SidecarID, ListenerName: t.ListenerName,
		})
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

func ListAnalyzerRuleTargets(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]SidecarRuleTarget, error) {
	var out []SidecarRuleTarget
	err := db.Raw(`
	SELECT sidecar_id, listener_name FROM private.ai_session_analyzer_rules_listeners
	WHERE org_id = ? AND analyzer_rule_name = ?
	ORDER BY sidecar_id, listener_name`, orgID, ruleName).Scan(&out).Error
	return out, err
}

func SidecarsBoundToAnalyzerRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	var out []string
	err := db.Raw(`SELECT DISTINCT sidecar_id FROM private.ai_session_analyzer_rules_listeners
	WHERE org_id = ? AND analyzer_rule_name = ?`, orgID, ruleName).Scan(&out).Error
	return out, err
}
