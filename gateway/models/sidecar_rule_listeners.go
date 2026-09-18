package models

import (
	"database/sql"
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
// configuration: which listener it goes on, and the block that listener gets.
//
// Spec is sidecar_spec verbatim -- the rule as the SIDECAR spells it, which is
// the block the lane receives rather than a translation of one. Composition
// places it; nothing converts it. The gateway's own columns beside it are that
// feature's, and this layer never reads them.
//
// One shape for all three features: they differ in what the block contains,
// not in how it is bound or delivered.
type BoundRule struct {
	RuleName     string
	ListenerName string
	Spec         json.RawMessage `gorm:"column:sidecar_spec"`
}

// SidecarRuleBinding names one rule bound to one listener, for the READ side.
//
// It carries no spec. The admin pages ask which rules a listener enforces, not
// what they say, and a list page that shipped every rule body would carry the
// whole fleet's policy to render three chips.
type SidecarRuleBinding struct {
	SidecarID    string `json:"sidecar_id"`
	Kind         string `json:"kind"`
	RuleName     string `json:"rule_name"`
	ListenerName string `json:"listener_name"`
}

// ListSidecarRuleBindings returns every rule bound to any sidecar in the
// organization, or to one sidecar when sidecarID is set.
//
// The admin pages need this because a bound rule is NEVER in the stored
// configuration: composition folds it into the served document on each
// handshake and stores nothing. A page reading the stored configuration alone
// shows a listener enforcing nothing while the sidecar enforces the rule --
// the control plane hiding its own work.
//
// One query over the three junctions rather than three per sidecar: the list
// page renders every sidecar at once.
func ListSidecarRuleBindings(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]SidecarRuleBinding, error) {
	var out []SidecarRuleBinding
	err := db.Raw(`
	SELECT sidecar_id, 'guardrail' AS kind, guardrail_rule_name AS rule_name, listener_name
	FROM private.guardrail_rules_listeners WHERE org_id = @org AND (@sc = '' OR sidecar_id::text = @sc)
	UNION ALL
	SELECT sidecar_id, 'datamasking', datamasking_rule_name, listener_name
	FROM private.datamasking_rules_listeners WHERE org_id = @org AND (@sc = '' OR sidecar_id::text = @sc)
	UNION ALL
	SELECT sidecar_id, 'analyzer', analyzer_rule_name, listener_name
	FROM private.ai_session_analyzer_rules_listeners WHERE org_id = @org AND (@sc = '' OR sidecar_id::text = @sc)
	ORDER BY sidecar_id, listener_name, kind, rule_name`,
		sql.Named("org", orgID), sql.Named("sc", sidecarID)).Scan(&out).Error
	return out, err
}

// ListGuardrailRulesForSidecar returns every guardrail rule bound to this
// sidecar, with the listener each one targets. Ordered by name so a composed
// document is byte-stable across calls: the served revision is a hash of it,
// and an unstable order would report every sidecar as lagging forever.
func ListGuardrailRulesForSidecar(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]BoundRule, error) {
	var out []BoundRule
	err := db.Raw(`
	SELECT b.listener_name, r.name AS rule_name, r.sidecar_spec
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

func ListDataMaskingRulesForSidecar(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]BoundRule, error) {
	var out []BoundRule
	err := db.Raw(`
	SELECT b.listener_name, r.name AS rule_name, r.sidecar_spec
	FROM private.datamasking_rules_listeners b
	JOIN private.datamasking_rules r ON r.org_id = b.org_id AND r.name = b.datamasking_rule_name
	WHERE b.org_id = ? AND b.sidecar_id = ?
	ORDER BY b.listener_name, r.name`, orgID, sidecarID).Scan(&out).Error
	return out, err
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
