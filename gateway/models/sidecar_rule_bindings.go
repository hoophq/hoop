package models

import (
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// What the three rule/listener junctions share, and the one read that spans
// them. Each junction entity has its own file beside this one.
//
// The three junctions. Separate tables rather than one polymorphic one,
// because only a real foreign key makes a deleted rule drop its bindings, and
// a polymorphic rule_name column cannot carry one. Same reasoning, and the
// same shape, as the *_rules_attributes junctions in migration 000068.
//
// The write and read-back halves, one per feature. Same replace-set semantics
// as the guardrail pair: the request carries the complete list the admin sees,
// so a target they removed must disappear rather than linger and keep being
// enforced.

// SidecarRuleTarget is one rule bound to one place on one sidecar.
//
// ListenerName empty targets the whole sidecar, which the control plane
// composes into the configuration's top-level block; the daemon concatenates
// that into every lane. A name targets that lane alone.
type SidecarRuleTarget struct {
	SidecarID    string `json:"sidecar_id"`
	ListenerName string `json:"listener_name"`
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
