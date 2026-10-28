package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// The guardrail junction, and the calls that bind it. One file per junction so
// a schema change for one binding stays local; the shared types, and why the
// three tables are separate, are in sidecar_rule_bindings.go.

type GuardrailRuleListener struct {
	OrgID        uuid.UUID `gorm:"column:org_id;primaryKey"`
	RuleName     string    `gorm:"column:guardrail_rule_name;primaryKey"`
	SidecarID    string    `gorm:"column:sidecar_id;primaryKey"`
	ListenerName string    `gorm:"column:listener_name;primaryKey"`
	Position     int       `gorm:"column:position"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (GuardrailRuleListener) TableName() string { return "private.guardrail_rules_listeners" }

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
	ORDER BY b.listener_name, b.position, r.name`, orgID, sidecarID).Scan(&out).Error
	return out, err
}

// SetGuardrailRuleListeners replaces the whole target set for one rule.
//
// Replace rather than merge: the request carries the complete list the admin
// sees, so a target they removed must disappear. Done in one transaction so a
// failed insert cannot leave the rule bound to nothing.
func SetGuardrailRuleListeners(db *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return SetGuardrailRuleListenersTx(tx, orgID, ruleName, targets)
	})
}

// SetGuardrailRuleListenersTx is the transaction-aware variant of
// SetGuardrailRuleListeners. It opens no transaction of its own, so the rule
// row and its bindings commit together or not at all.
//
// Separate transactions are what the API must not do here. The write gate
// checks the INCOMING spec against the INCOMING target set, so a rule row that
// commits before its bindings fail leaves the OLD bindings serving the NEW
// spec -- the one combination the gate refuses.
func SetGuardrailRuleListenersTx(tx *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	return setRuleListenersTx(tx, "private.guardrail_rules_listeners", "guardrail_rule_name", orgID, ruleName, targets)
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
