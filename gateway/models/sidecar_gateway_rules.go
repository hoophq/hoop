package models

import (
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// A gateway rule reaches a sidecar listener through the listener's MIRROR
// connection, by the two bindings an agent connection uses: the rule names
// the connection (the *_rules_connections junction), or the rule and the
// connection share an attribute. The reads here answer "which gateway rules
// reach which listener of this sidecar" with exactly that semantics, so a rule
// reaches a mirror when it would reach an agent connection of the same shape
// (GetConnectionGuardRailRulesByConnectionAndAttribute,
// GetDataMaskingEntityTypesByConnectionAndAttributes).

// MirrorGuardrailRule is one gateway guardrail rule on one listener, in the
// gateway's own vocabulary. The control plane translates it.
type MirrorGuardrailRule struct {
	RuleName     string          `gorm:"column:rule_name"`
	ListenerName string          `gorm:"column:listener_name"`
	Input        json.RawMessage `gorm:"column:input"`
	Output       json.RawMessage `gorm:"column:output"`
}

// MirrorDataMaskingRule is one gateway data masking rule on one listener.
type MirrorDataMaskingRule struct {
	RuleName             string                   `gorm:"column:rule_name"`
	ListenerName         string                   `gorm:"column:listener_name"`
	SupportedEntityTypes SupportedEntityTypesList `gorm:"column:supported_entity_types;serializer:json"`
	CustomEntityTypes    CustomEntityTypesList    `gorm:"column:custom_entity_types;serializer:json"`
	ScoreThreshold       *float64                 `gorm:"column:score_threshold"`
}

// mirrorsReachedByGuardrail is the join between a guardrail rule r and the
// mirror connections c it reaches. Both sides of the OR are the agent's.
const mirrorsReachedByGuardrail = `(
	EXISTS (SELECT 1 FROM private.guardrail_rules_connections grc
		WHERE grc.rule_id = r.id AND grc.connection_id = c.id)
	OR EXISTS (SELECT 1 FROM private.guardrail_rules_attributes gra
		JOIN private.connections_attributes ca
		  ON ca.org_id = gra.org_id AND ca.attribute_name = gra.attribute_name
		WHERE gra.org_id = r.org_id AND gra.guardrail_rule_name = r.name AND ca.connection_name = c.name)
)`

// mirrorsReachedByDataMasking is the same join for a data masking rule. The
// status filter is the one the agent's read applies.
const mirrorsReachedByDataMasking = `(
	EXISTS (SELECT 1 FROM private.datamasking_rules_connections drc
		WHERE drc.rule_id = r.id AND drc.connection_id = c.id AND drc.status = 'active')
	OR EXISTS (SELECT 1 FROM private.datamasking_rules_attributes dra
		JOIN private.connections_attributes ca
		  ON ca.org_id = dra.org_id AND ca.attribute_name = dra.attribute_name
		WHERE dra.org_id = r.org_id AND dra.datamasking_rule_name = r.name AND ca.connection_name = c.name)
)`

// ListMirrorGuardrailRules returns every gateway guardrail rule that reaches a
// listener of this sidecar through its mirror. Ordered so the served document
// is byte-stable across calls, as ListGuardrailRulesForSidecar is.
func ListMirrorGuardrailRules(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]MirrorGuardrailRule, error) {
	var out []MirrorGuardrailRule
	err := db.Raw(`
	SELECT c.sidecar_listener AS listener_name, r.name AS rule_name, r.input, r.output
	FROM private.connections c
	JOIN private.guardrail_rules r ON r.org_id = c.org_id
	WHERE c.org_id = @org AND c.sidecar_id = @sc AND c.sidecar_listener IS NOT NULL
	  AND `+mirrorsReachedByGuardrail+`
	ORDER BY c.sidecar_listener, r.name`,
		sql.Named("org", orgID), sql.Named("sc", sidecarID)).Scan(&out).Error
	return out, err
}

// ListMirrorDataMaskingRules is ListMirrorGuardrailRules for data masking.
func ListMirrorDataMaskingRules(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]MirrorDataMaskingRule, error) {
	var out []MirrorDataMaskingRule
	err := db.Raw(`
	SELECT c.sidecar_listener AS listener_name, r.name AS rule_name,
	       r.supported_entity_types, r.custom_entity_types, r.score_threshold
	FROM private.connections c
	JOIN private.datamasking_rules r ON r.org_id = c.org_id
	WHERE c.org_id = @org AND c.sidecar_id = @sc AND c.sidecar_listener IS NOT NULL
	  AND `+mirrorsReachedByDataMasking+`
	ORDER BY c.sidecar_listener, r.name`,
		sql.Named("org", orgID), sql.Named("sc", sidecarID)).Scan(&out).Error
	return out, err
}

// SidecarsReachedByGuardrailRule names the sidecars one gateway guardrail rule
// reaches through their mirrors. The rule's write re-composes each of them.
func SidecarsReachedByGuardrailRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	var out []string
	err := db.Raw(`
	SELECT DISTINCT c.sidecar_id
	FROM private.connections c
	JOIN private.guardrail_rules r ON r.org_id = c.org_id
	WHERE c.org_id = @org AND r.name = @rule AND c.sidecar_id IS NOT NULL
	  AND `+mirrorsReachedByGuardrail,
		sql.Named("org", orgID), sql.Named("rule", ruleName)).Scan(&out).Error
	return out, err
}

// SidecarsReachedByDataMaskingRule is SidecarsReachedByGuardrailRule for data
// masking.
func SidecarsReachedByDataMaskingRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	var out []string
	err := db.Raw(`
	SELECT DISTINCT c.sidecar_id
	FROM private.connections c
	JOIN private.datamasking_rules r ON r.org_id = c.org_id
	WHERE c.org_id = @org AND r.name = @rule AND c.sidecar_id IS NOT NULL
	  AND `+mirrorsReachedByDataMasking,
		sql.Named("org", orgID), sql.Named("rule", ruleName)).Scan(&out).Error
	return out, err
}

// ListMirrorRuleBindings names every gateway rule reaching a listener of any
// sidecar in the organization, or of one sidecar when sidecarID is set, in the
// shape of ListSidecarRuleBindings. The Sidecars page lists both: a listener
// enforces a translated rule as it enforces a native one.
func ListMirrorRuleBindings(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]SidecarRuleBinding, error) {
	var out []SidecarRuleBinding
	err := db.Raw(`
	SELECT c.sidecar_id, 'guardrail' AS kind, r.name AS rule_name, c.sidecar_listener AS listener_name
	FROM private.connections c
	JOIN private.guardrail_rules r ON r.org_id = c.org_id
	WHERE c.org_id = @org AND c.sidecar_listener IS NOT NULL AND (@sc = '' OR c.sidecar_id::text = @sc)
	  AND `+mirrorsReachedByGuardrail+`
	UNION ALL
	SELECT c.sidecar_id, 'datamasking', r.name, c.sidecar_listener
	FROM private.connections c
	JOIN private.datamasking_rules r ON r.org_id = c.org_id
	WHERE c.org_id = @org AND c.sidecar_listener IS NOT NULL AND (@sc = '' OR c.sidecar_id::text = @sc)
	  AND `+mirrorsReachedByDataMasking+`
	ORDER BY sidecar_id, listener_name, kind, rule_name`,
		sql.Named("org", orgID), sql.Named("sc", sidecarID)).Scan(&out).Error
	return out, err
}
