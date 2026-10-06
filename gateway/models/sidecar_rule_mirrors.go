package models

import (
	"database/sql"
	"slices"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Where a sidecar rule binding is stored. Expand phase of a move from the
// listener name to the listener's mirror connection (000133):
//
//   - *_rules_listeners is the source of truth. A gateway older than 000133
//     reads and writes only that table, and during a rolling deploy or after
//     an image rollback its writes must count, a removed binding included.
//     The served document and every read come from it.
//   - *_rules_mirrors holds the same binding on the mirror, the resource the
//     admin sees. Every write here keeps it in step, and
//     SyncSidecarBindingsToMirrorsTx repairs what an older gateway changed.
//
// The mirror rows are not in *_rules_connections: those carry a rule's
// connection_ids, a public API field, and keep their meaning.
//
// The contract phase, once no older gateway runs, reads the mirror rows and
// drops the listener rows of mirrored listeners.

// sidecarRuleJunction names the tables of one rule kind. Every value is a
// constant below, never input, so the queries can splice them. Every table
// keys the rule by name in ruleCol.
type sidecarRuleJunction struct {
	kind, rules, ruleCol, listeners, mirrors, attributes string
	// rulepackFree and gatewayBound are SQL over the rule row r for the
	// detach: the rule has no rulepack, and the rule is bound to a gateway
	// connection.
	rulepackFree, gatewayBound string
}

var (
	guardrailJunction = sidecarRuleJunction{
		kind:         "guardrail",
		rules:        "private.guardrail_rules",
		ruleCol:      "guardrail_rule_name",
		listeners:    "private.guardrail_rules_listeners",
		mirrors:      "private.guardrail_rules_mirrors",
		attributes:   "private.guardrail_rules_attributes",
		rulepackFree: "r.rulepack_id IS NULL",
		gatewayBound: "EXISTS (SELECT 1 FROM private.guardrail_rules_connections c WHERE c.rule_id = r.id)",
	}
	datamaskingJunction = sidecarRuleJunction{
		kind:         "datamasking",
		rules:        "private.datamasking_rules",
		ruleCol:      "datamasking_rule_name",
		listeners:    "private.datamasking_rules_listeners",
		mirrors:      "private.datamasking_rules_mirrors",
		attributes:   "private.datamasking_rules_attributes",
		rulepackFree: "r.rulepack_id IS NULL",
		gatewayBound: "EXISTS (SELECT 1 FROM private.datamasking_rules_connections c WHERE c.rule_id = r.id)",
	}
	analyzerJunction = sidecarRuleJunction{
		kind:         "analyzer",
		rules:        "private.ai_session_analyzer_rules",
		ruleCol:      "analyzer_rule_name",
		listeners:    "private.ai_session_analyzer_rules_listeners",
		mirrors:      "private.ai_session_analyzer_rules_mirrors",
		attributes:   "private.ai_session_analyzer_rules_attributes",
		rulepackFree: "TRUE",
		// The gateway binds an analyzer by connection name, not by junction.
		gatewayBound: "COALESCE(array_length(r.connection_names, 1), 0) > 0",
	}
	sidecarRuleJunctions = []sidecarRuleJunction{guardrailJunction, datamaskingJunction, analyzerJunction}
)

// boundRows is every sidecar binding of this kind in the org @org:
// sidecar_id, listener_name, position, rule_name, sidecar_spec.
func (j sidecarRuleJunction) boundRows() string {
	return `
	SELECT b.sidecar_id::text AS sidecar_id, b.listener_name, b.position, r.name AS rule_name, r.sidecar_spec
	FROM ` + j.listeners + ` b
	JOIN ` + j.rules + ` r ON r.org_id = b.org_id AND r.name = b.` + j.ruleCol + `
	WHERE b.org_id = @org`
}

// listRulesForSidecar returns every rule of this kind bound to one sidecar.
// Ordered by listener, then position, then name, so a composed document is
// byte-stable across calls: the served revision is a hash of it, and an
// unstable order would report every sidecar as lagging forever.
func (j sidecarRuleJunction) listRulesForSidecar(db *gorm.DB, orgID uuid.UUID, sidecarID string) ([]BoundRule, error) {
	var out []BoundRule
	err := db.Raw(`SELECT listener_name, rule_name, sidecar_spec FROM (`+j.boundRows()+`) b
	WHERE sidecar_id = @sc ORDER BY listener_name, position, rule_name`,
		sql.Named("org", orgID), sql.Named("sc", sidecarID)).Scan(&out).Error
	return out, err
}

// listTargets returns where one rule is bound, with each place's position.
func (j sidecarRuleJunction) listTargets(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]storedSidecarTarget, error) {
	var out []storedSidecarTarget
	err := db.Raw(`SELECT sidecar_id, listener_name, position FROM (`+j.boundRows()+`) b
	WHERE rule_name = @rule ORDER BY sidecar_id, listener_name`,
		sql.Named("org", orgID), sql.Named("rule", ruleName)).Scan(&out).Error
	return out, err
}

func (j sidecarRuleJunction) ruleTargets(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]SidecarRuleTarget, error) {
	stored, err := j.listTargets(db, orgID, ruleName)
	if err != nil {
		return nil, err
	}
	out := make([]SidecarRuleTarget, 0, len(stored))
	for _, s := range stored {
		out = append(out, SidecarRuleTarget{SidecarID: s.SidecarID, ListenerName: s.ListenerName})
	}
	return out, nil
}

func (j sidecarRuleJunction) sidecarsBoundToRule(db *gorm.DB, orgID uuid.UUID, ruleName string) ([]string, error) {
	var out []string
	err := db.Raw(`SELECT DISTINCT sidecar_id FROM (`+j.boundRows()+`) b WHERE rule_name = @rule`,
		sql.Named("org", orgID), sql.Named("rule", ruleName)).Scan(&out).Error
	return out, err
}

type storedSidecarTarget struct {
	SidecarID    string
	ListenerName string
	Position     int
}

// setTargetsTx replaces one rule's targets. Every target goes in the listener
// table, and also on its listener's mirror when the listener has one.
//
// A target the rule already had keeps its position, so saving a rule does not
// move it to the end of its listener. A new target without a position goes
// last.
func (j sidecarRuleJunction) setTargetsTx(tx *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	stored, err := j.listTargets(tx, orgID, ruleName)
	if err != nil {
		return err
	}
	// The lock the mirror writer takes. Without it, a mirror made while this
	// runs can miss a binding this writes, and the reverse.
	sidecars := make([]string, 0, len(stored)+len(targets))
	for _, s := range stored {
		sidecars = append(sidecars, s.SidecarID)
	}
	for _, t := range targets {
		sidecars = append(sidecars, t.SidecarID)
	}
	if err := lockSidecarsTx(tx, orgID, sidecars); err != nil {
		return err
	}
	kept := map[string]int{}
	for _, s := range stored {
		kept[s.SidecarID+"/"+s.ListenerName] = s.Position
	}
	err = tx.Exec(`DELETE FROM `+j.listeners+` WHERE org_id = ? AND `+j.ruleCol+` = ?`, orgID, ruleName).Error
	if err != nil {
		return err
	}
	err = tx.Exec(`DELETE FROM `+j.mirrors+` WHERE org_id = ? AND `+j.ruleCol+` = ?`, orgID, ruleName).Error
	if err != nil {
		return err
	}
	for _, t := range targets {
		var mirrorID string
		err = tx.Raw(`SELECT id FROM private.connections WHERE org_id = ? AND sidecar_id = ? AND sidecar_listener = ?`,
			orgID, t.SidecarID, t.ListenerName).Scan(&mirrorID).Error
		if err != nil {
			return err
		}
		var pos int
		switch p, ok := kept[t.SidecarID+"/"+t.ListenerName]; {
		case t.Position != nil:
			pos = *t.Position
		case ok:
			pos = p
		default:
			err = tx.Raw(`SELECT COALESCE(MAX(position), -1) + 1 FROM `+j.listeners+
				` WHERE org_id = ? AND sidecar_id = ? AND listener_name = ?`,
				orgID, t.SidecarID, t.ListenerName).Scan(&pos).Error
		}
		if err != nil {
			return err
		}
		err = tx.Exec(`INSERT INTO `+j.listeners+` (org_id, `+j.ruleCol+`, sidecar_id, listener_name, position)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, orgID, ruleName, t.SidecarID, t.ListenerName, pos).Error
		if err != nil {
			return err
		}
		if mirrorID == "" {
			continue
		}
		err = tx.Exec(`INSERT INTO `+j.mirrors+` (org_id, `+j.ruleCol+`, connection_id, position)
		VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, orgID, ruleName, mirrorID, pos).Error
		if err != nil {
			return err
		}
	}
	return nil
}

// lockSidecarsTx locks the sidecar rows in id order, so two writes that touch
// the same sidecars cannot deadlock.
func lockSidecarsTx(tx *gorm.DB, orgID uuid.UUID, sidecarIDs []string) error {
	slices.Sort(sidecarIDs)
	sidecarIDs = slices.Compact(sidecarIDs)
	if len(sidecarIDs) == 0 {
		return nil
	}
	var locked []string
	return tx.Raw(`SELECT id FROM private.sidecars WHERE org_id = ? AND id::text IN ? ORDER BY id FOR UPDATE`,
		orgID, sidecarIDs).Scan(&locked).Error
}

// syncMirrorsTx makes the mirror rows of one sidecar match its listener
// rows: it adds a missing one, takes the listener row's position, and deletes
// one whose listener row is gone (an older gateway removed the binding).
func (j sidecarRuleJunction) syncMirrorsTx(tx *gorm.DB, orgID, sidecarID string) error {
	err := tx.Exec(`
	DELETE FROM `+j.mirrors+` m USING private.connections c
	WHERE c.id = m.connection_id AND c.org_id = ? AND c.sidecar_id = ?
	  AND NOT EXISTS (SELECT 1 FROM `+j.listeners+` b
		WHERE b.org_id = m.org_id AND b.`+j.ruleCol+` = m.`+j.ruleCol+`
		  AND b.sidecar_id = c.sidecar_id AND b.listener_name = c.sidecar_listener)`, orgID, sidecarID).Error
	if err != nil {
		return err
	}
	return tx.Exec(`
	INSERT INTO `+j.mirrors+` (org_id, `+j.ruleCol+`, connection_id, position)
	SELECT b.org_id, b.`+j.ruleCol+`, c.id, b.position
	FROM `+j.listeners+` b
	JOIN private.connections c
	  ON c.org_id = b.org_id AND c.sidecar_id = b.sidecar_id AND c.sidecar_listener = b.listener_name
	WHERE b.org_id = ? AND b.sidecar_id = ?
	ON CONFLICT (org_id, `+j.ruleCol+`, connection_id) DO UPDATE SET position = EXCLUDED.position`, orgID, sidecarID).Error
}

// SyncSidecarBindingsToMirrorsTx makes the mirror rows of one sidecar match
// its listener rows, for every rule kind. The mirror writer calls it after
// each write, so a mirror made after 000133 (a fallback name, an org that
// turns beta.sidecar_listeners on) takes the bindings of its listener, and a
// change an older gateway made reaches the mirror.
func SyncSidecarBindingsToMirrorsTx(tx *gorm.DB, orgID, sidecarID string) error {
	for _, j := range sidecarRuleJunctions {
		if err := j.syncMirrorsTx(tx, orgID, sidecarID); err != nil {
			return err
		}
	}
	return nil
}
