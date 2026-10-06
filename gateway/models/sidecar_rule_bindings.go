package models

import (
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

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
	// Position is the rule's place in its listener, where the first match
	// wins. Nil keeps the stored place, or puts a new target last.
	Position *int `json:"-"`
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
// One query over every junction rather than three per sidecar: the list
// page renders every sidecar at once.
func ListSidecarRuleBindings(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]SidecarRuleBinding, error) {
	parts := make([]string, 0, len(sidecarRuleJunctions))
	for _, j := range sidecarRuleJunctions {
		parts = append(parts, `SELECT sidecar_id, '`+j.kind+`' AS kind, rule_name, listener_name FROM (`+
			j.boundRows()+`) b WHERE @sc = '' OR sidecar_id = @sc`)
	}
	var out []SidecarRuleBinding
	err := db.Raw(strings.Join(parts, " UNION ALL ")+` ORDER BY sidecar_id, listener_name, kind, rule_name`,
		sql.Named("org", orgID), sql.Named("sc", sidecarID)).Scan(&out).Error
	return out, err
}

// DetachedSidecarRules names what DetachSidecarRulesTx did, per kind: the
// rules it deleted and the rules it only unbound.
type DetachedSidecarRules struct {
	Guardrails []string
	Masking    []string
	Analyzers  []string
	Unbound    struct {
		Guardrails []string
		Masking    []string
		Analyzers  []string
	}
}

// DetachSidecarRulesTx removes every binding to one sidecar, on its listeners
// and on its mirrors. It deletes a rule only when that sidecar's config file
// brought it (imported_from_sidecar), no rulepack owns it, and no target is
// left: no other listener, no connection and no attribute. Every other rule is
// only unbound, so a rule an admin wrote survives the switch.
func DetachSidecarRulesTx(tx *gorm.DB, orgID uuid.UUID, sidecarID string) (DetachedSidecarRules, error) {
	var out DetachedSidecarRules
	outputs := map[string][2]*[]string{
		"guardrail":   {&out.Guardrails, &out.Unbound.Guardrails},
		"datamasking": {&out.Masking, &out.Unbound.Masking},
		"analyzer":    {&out.Analyzers, &out.Unbound.Analyzers},
	}
	for _, j := range sidecarRuleJunctions {
		var unbound, onMirrors []string
		err := tx.Raw(`DELETE FROM `+j.listeners+` WHERE org_id = ? AND sidecar_id = ? RETURNING `+j.ruleCol,
			orgID, sidecarID).Scan(&unbound).Error
		if err != nil {
			return out, err
		}
		err = tx.Raw(`DELETE FROM `+j.mirrors+` m USING private.connections c
		WHERE c.id = m.connection_id AND c.org_id = ? AND c.sidecar_id = ?
		RETURNING m.`+j.ruleCol, orgID, sidecarID).Scan(&onMirrors).Error
		if err != nil {
			return out, err
		}
		unbound = append(unbound, onMirrors...)
		if len(unbound) == 0 {
			continue
		}
		var deleted []string
		err = tx.Raw(`
		DELETE FROM `+j.rules+` r
		WHERE r.org_id = ? AND r.name IN ? AND r.imported_from_sidecar = ? AND `+j.rulepackFree+`
		  AND NOT EXISTS (SELECT 1 FROM `+j.listeners+` l WHERE l.org_id = r.org_id AND l.`+j.ruleCol+` = r.name)
		  AND NOT EXISTS (SELECT 1 FROM `+j.mirrors+` m WHERE m.org_id = r.org_id AND m.`+j.ruleCol+` = r.name)
		  AND NOT EXISTS (SELECT 1 FROM `+j.attributes+` a WHERE a.org_id = r.org_id AND a.`+j.ruleCol+` = r.name)
		  AND NOT (`+j.gatewayBound+`)
		RETURNING r.name`, orgID, unbound, sidecarID).Scan(&deleted).Error
		if err != nil {
			return out, err
		}
		deletedOut, unboundOut := outputs[j.kind][0], outputs[j.kind][1]
		// Sorted, so the answer the admin reads is stable.
		slices.Sort(deleted)
		slices.Sort(unbound)
		*deletedOut = deleted
		gone := map[string]bool{}
		for _, n := range deleted {
			gone[n] = true
		}
		seen := map[string]bool{}
		for _, n := range unbound {
			if !gone[n] && !seen[n] {
				seen[n] = true
				*unboundOut = append(*unboundOut, n)
			}
		}
	}
	return out, nil
}

// MarkImportedRuleTx records the sidecar whose config file brought a rule.
func MarkImportedRuleTx(tx *gorm.DB, table string, orgID uuid.UUID, ruleName, sidecarID string) error {
	return tx.Exec(`UPDATE `+table+` SET imported_from_sidecar = ? WHERE org_id = ? AND name = ?`,
		sidecarID, orgID, ruleName).Error
}
