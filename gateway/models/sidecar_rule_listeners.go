package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SidecarRuleTarget is one rule bound to one place on one sidecar.
//
// ListenerName empty targets the whole sidecar, which the control plane
// composes into the configuration's top-level block; the daemon concatenates
// that into every lane. A name targets that lane alone.
type SidecarRuleTarget struct {
	SidecarID    string `json:"sidecar_id"`
	ListenerName string `json:"listener_name"`
}

// The three junctions. Separate tables rather than one polymorphic one,
// because only a real foreign key makes a deleted rule drop its bindings, and
// a polymorphic rule_name column cannot carry one. Same reasoning, and the
// same shape, as the *_rules_attributes junctions in migration 000068.

type GuardrailRuleListener struct {
	OrgID        uuid.UUID `gorm:"column:org_id;primaryKey"`
	RuleName     string    `gorm:"column:guardrail_rule_name;primaryKey"`
	SidecarID    string    `gorm:"column:sidecar_id;primaryKey"`
	ListenerName string    `gorm:"column:listener_name;primaryKey"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (GuardrailRuleListener) TableName() string { return "private.guardrail_rules_listeners" }

type DatamaskingRuleListener struct {
	OrgID        uuid.UUID `gorm:"column:org_id;primaryKey"`
	RuleName     string    `gorm:"column:datamasking_rule_name;primaryKey"`
	SidecarID    string    `gorm:"column:sidecar_id;primaryKey"`
	ListenerName string    `gorm:"column:listener_name;primaryKey"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (DatamaskingRuleListener) TableName() string { return "private.datamasking_rules_listeners" }

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

// BoundRule is a rule the control plane must fold into one sidecar's served
// configuration: where it goes, and the rule's own stored content.
//
// Content is the raw column the feature stores, so the translators in
// gateway/services read one shape and this layer stays ignorant of rule
// vocabulary.
type BoundRule struct {
	RuleName     string
	ListenerName string
	Input        json.RawMessage `gorm:"column:input"`
	Output       json.RawMessage `gorm:"column:output"`
}

// ListGuardrailRulesForSidecar returns every guardrail rule bound to this
// sidecar, with the listener each one targets. Ordered by name so a composed
// document is byte-stable across calls: the served revision is a hash of it,
// and an unstable order would report every sidecar as lagging forever.
func ListGuardrailRulesForSidecar(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]BoundRule, error) {
	var out []BoundRule
	err := db.Raw(`
	SELECT b.listener_name, r.name AS rule_name, r.input, r.output
	FROM private.guardrail_rules_listeners b
	JOIN private.guardrail_rules r ON r.org_id = b.org_id AND r.name = b.guardrail_rule_name
	WHERE b.org_id = ? AND b.sidecar_id = ?
	ORDER BY b.listener_name, r.name`, orgID, sidecarID).Scan(&out).Error
	return out, err
}

// SetGuardrailRuleListeners replaces the whole target set for one rule.
//
// Replace rather than merge: the request carries the complete list the admin
// sees, so a target they removed must disappear. Done in one transaction so a
// failed insert cannot leave the rule bound to nothing.
func SetGuardrailRuleListeners(db *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return db.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("org_id = ? AND guardrail_rule_name = ?", orgID, ruleName).
			Delete(&GuardrailRuleListener{}).Error
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil
		}
		rows := make([]GuardrailRuleListener, 0, len(targets))
		for _, t := range targets {
			rows = append(rows, GuardrailRuleListener{
				OrgID: orgID, RuleName: ruleName,
				SidecarID: t.SidecarID, ListenerName: t.ListenerName,
			})
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	})
}

// ListGuardrailRuleTargets returns where one rule is bound, for the API to
// render back what was saved.
func ListGuardrailRuleTargets(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]SidecarRuleTarget, error) {
	var out []SidecarRuleTarget
	err := db.Raw(`
	SELECT sidecar_id, listener_name
	FROM private.guardrail_rules_listeners
	WHERE org_id = ? AND guardrail_rule_name = ?
	ORDER BY sidecar_id, listener_name`, orgID, ruleName).Scan(&out).Error
	return out, err
}

// SidecarsBoundToGuardrailRule names the sidecars a rule reaches. The write
// guards re-run against each of them whenever the rule changes, so a rule
// edited out of what a sidecar can enforce is refused rather than served.
func SidecarsBoundToGuardrailRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	var out []string
	err := db.Raw(`
	SELECT DISTINCT sidecar_id
	FROM private.guardrail_rules_listeners
	WHERE org_id = ? AND guardrail_rule_name = ?`, orgID, ruleName).Scan(&out).Error
	return out, err
}

// MaskBinding is one data masking rule bound to a sidecar, with the entity
// groups the translator flattens.
type MaskBinding struct {
	RuleName             string
	ListenerName         string
	SupportedEntityTypes SupportedEntityTypesList `gorm:"column:supported_entity_types;serializer:json"`
	ScoreThreshold       *float64                 `gorm:"column:score_threshold"`
}

// AnalyzerBinding is one analyzer rule bound to a sidecar listener.
type AnalyzerBinding struct {
	RuleName       string
	ListenerName   string
	RiskEvaluation AISessionAnalyzerRiskEvaluation `gorm:"column:risk_evaluation;serializer:json"`
	CustomPrompt   *string                         `gorm:"column:custom_prompt"`
}

func ListDataMaskingRulesForSidecar(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]MaskBinding, error) {
	var out []MaskBinding
	err := db.Raw(`
	SELECT b.listener_name, r.name AS rule_name, r.supported_entity_types, r.score_threshold
	FROM private.datamasking_rules_listeners b
	JOIN private.datamasking_rules r ON r.org_id = b.org_id AND r.name = b.datamasking_rule_name
	WHERE b.org_id = ? AND b.sidecar_id = ?
	ORDER BY b.listener_name, r.name`, orgID, sidecarID).Scan(&out).Error
	return out, err
}

func ListAnalyzerRulesForSidecar(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]AnalyzerBinding, error) {
	var out []AnalyzerBinding
	err := db.Raw(`
	SELECT b.listener_name, r.name AS rule_name, r.risk_evaluation, r.custom_prompt
	FROM private.ai_session_analyzer_rules_listeners b
	JOIN private.ai_session_analyzer_rules r ON r.org_id = b.org_id AND r.name = b.analyzer_rule_name
	WHERE b.org_id = ? AND b.sidecar_id = ?
	ORDER BY b.listener_name, r.name`, orgID, sidecarID).Scan(&out).Error
	return out, err
}

// The write and read-back halves, one per feature. Same replace-set semantics
// as the guardrail pair: the request carries the complete list the admin sees,
// so a target they removed must disappear rather than linger and keep being
// enforced.

func SetDataMaskingRuleListeners(db *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return db.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("org_id = ? AND datamasking_rule_name = ?", orgID, ruleName).
			Delete(&DatamaskingRuleListener{}).Error
		if err != nil || len(targets) == 0 {
			return err
		}
		rows := make([]DatamaskingRuleListener, 0, len(targets))
		for _, t := range targets {
			rows = append(rows, DatamaskingRuleListener{
				OrgID: orgID, RuleName: ruleName,
				SidecarID: t.SidecarID, ListenerName: t.ListenerName,
			})
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	})
}

func SetAnalyzerRuleListeners(db *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return db.Transaction(func(tx *gorm.DB) error {
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
	})
}

func ListDataMaskingRuleTargets(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]SidecarRuleTarget, error) {
	var out []SidecarRuleTarget
	err := db.Raw(`
	SELECT sidecar_id, listener_name FROM private.datamasking_rules_listeners
	WHERE org_id = ? AND datamasking_rule_name = ?
	ORDER BY sidecar_id, listener_name`, orgID, ruleName).Scan(&out).Error
	return out, err
}

func ListAnalyzerRuleTargets(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]SidecarRuleTarget, error) {
	var out []SidecarRuleTarget
	err := db.Raw(`
	SELECT sidecar_id, listener_name FROM private.ai_session_analyzer_rules_listeners
	WHERE org_id = ? AND analyzer_rule_name = ?
	ORDER BY sidecar_id, listener_name`, orgID, ruleName).Scan(&out).Error
	return out, err
}

func SidecarsBoundToDataMaskingRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	var out []string
	err := db.Raw(`SELECT DISTINCT sidecar_id FROM private.datamasking_rules_listeners
	WHERE org_id = ? AND datamasking_rule_name = ?`, orgID, ruleName).Scan(&out).Error
	return out, err
}

func SidecarsBoundToAnalyzerRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	var out []string
	err := db.Raw(`SELECT DISTINCT sidecar_id FROM private.ai_session_analyzer_rules_listeners
	WHERE org_id = ? AND analyzer_rule_name = ?`, orgID, ruleName).Scan(&out).Error
	return out, err
}
