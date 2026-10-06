package models

import (
	"database/sql"
	"errors"
	"maps"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Where a sidecar rule binding is stored. A listener with a mirror connection
// keeps its bindings on the mirror, in the rule's connection junction. A
// listener without one (an org with beta.sidecar_listeners off) keeps them in
// the *_rules_listeners table. SetSidecarRuleTargetsTx and
// MoveSidecarBindingsToMirrorsTx hold that up; every read unions both stores,
// so the served document does not depend on where a row sits.

// ErrSidecarMirrorRuleBinding is a write that changes the rules of a mirror
// connection through a connection list. The sidecar path owns those rows: it
// checks every binding against what the sidecar can run before it stores it.
var ErrSidecarMirrorRuleBinding = errors.New("the connection mirrors a sidecar listener; bind a rule to it through the rule's sidecar_targets")

// sidecarRuleJunction names the tables of one rule kind. Every value is a
// constant below, never input, so the queries can splice them.
type sidecarRuleJunction struct {
	kind        string
	rules       string
	listeners   string
	listenerCol string
	attributes  string
	// mirrors is the connection junction. mirrorJoin joins its row m to the
	// rule row r; mirrorRuleCol and mirrorRuleValue are the rule key it holds.
	mirrors, mirrorJoin, mirrorRuleCol, mirrorRuleValue string
	// rulepackFree and namedConnections are SQL over r for the detach.
	rulepackFree, namedConnections string
}

var (
	guardrailJunction = sidecarRuleJunction{
		kind:             "guardrail",
		rules:            "private.guardrail_rules",
		listeners:        "private.guardrail_rules_listeners",
		listenerCol:      "guardrail_rule_name",
		attributes:       "private.guardrail_rules_attributes",
		mirrors:          "private.guardrail_rules_connections",
		mirrorJoin:       "r.id = m.rule_id",
		mirrorRuleCol:    "rule_id",
		mirrorRuleValue:  "r.id",
		rulepackFree:     "r.rulepack_id IS NULL",
		namedConnections: "FALSE",
	}
	datamaskingJunction = sidecarRuleJunction{
		kind:             "datamasking",
		rules:            "private.datamasking_rules",
		listeners:        "private.datamasking_rules_listeners",
		listenerCol:      "datamasking_rule_name",
		attributes:       "private.datamasking_rules_attributes",
		mirrors:          "private.datamasking_rules_connections",
		mirrorJoin:       "r.id = m.rule_id",
		mirrorRuleCol:    "rule_id",
		mirrorRuleValue:  "r.id",
		rulepackFree:     "r.rulepack_id IS NULL",
		namedConnections: "FALSE",
	}
	analyzerJunction = sidecarRuleJunction{
		kind:            "analyzer",
		rules:           "private.ai_session_analyzer_rules",
		listeners:       "private.ai_session_analyzer_rules_listeners",
		listenerCol:     "analyzer_rule_name",
		attributes:      "private.ai_session_analyzer_rules_attributes",
		mirrors:         "private.ai_session_analyzer_rules_connections",
		mirrorJoin:      "r.org_id = m.org_id AND r.name = m.analyzer_rule_name",
		mirrorRuleCol:   "analyzer_rule_name",
		mirrorRuleValue: "r.name",
		rulepackFree:    "TRUE",
		// The gateway binds an analyzer by connection name, not by junction.
		namedConnections: "COALESCE(array_length(r.connection_names, 1), 0) > 0",
	}
	sidecarRuleJunctions = []sidecarRuleJunction{guardrailJunction, datamaskingJunction, analyzerJunction}
)

// boundRows is every sidecar binding of this kind in the org @org, from both
// stores: sidecar_id, listener_name, position, rule_name, sidecar_spec.
func (j sidecarRuleJunction) boundRows() string {
	return `
	SELECT b.sidecar_id::text AS sidecar_id, b.listener_name, b.position, r.name AS rule_name, r.sidecar_spec
	FROM ` + j.listeners + ` b
	JOIN ` + j.rules + ` r ON r.org_id = b.org_id AND r.name = b.` + j.listenerCol + `
	WHERE b.org_id = @org
	UNION ALL
	SELECT c.sidecar_id::text, c.sidecar_listener, m.position, r.name, r.sidecar_spec
	FROM ` + j.mirrors + ` m
	JOIN ` + j.rules + ` r ON ` + j.mirrorJoin + `
	JOIN private.connections c ON c.id = m.connection_id AND c.org_id = r.org_id
	WHERE r.org_id = @org AND c.sidecar_id IS NOT NULL`
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

// setTargetsTx replaces one rule's targets. A target goes on its listener's
// mirror when the listener has one, and in the listener table otherwise.
//
// A target the rule already had keeps its position, so saving a rule does not
// move it to the end of its listener. A new target without a position goes
// last.
func (j sidecarRuleJunction) setTargetsTx(tx *gorm.DB, orgID uuid.UUID, ruleName string, targets []SidecarRuleTarget) error {
	stored, err := j.listTargets(tx, orgID, ruleName)
	if err != nil {
		return err
	}
	kept := map[string]int{}
	for _, s := range stored {
		kept[s.SidecarID+"/"+s.ListenerName] = s.Position
	}
	err = tx.Exec(`DELETE FROM `+j.listeners+` WHERE org_id = ? AND `+j.listenerCol+` = ?`, orgID, ruleName).Error
	if err != nil {
		return err
	}
	err = tx.Exec(`DELETE FROM `+j.mirrors+` m USING `+j.rules+` r, private.connections c
	WHERE `+j.mirrorJoin+` AND c.id = m.connection_id AND c.sidecar_id IS NOT NULL
	  AND r.org_id = ? AND r.name = ?`, orgID, ruleName).Error
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
		case mirrorID != "":
			err = tx.Raw(`SELECT COALESCE(MAX(position), -1) + 1 FROM `+j.mirrors+` WHERE connection_id = ?`,
				mirrorID).Scan(&pos).Error
		default:
			err = tx.Raw(`SELECT COALESCE(MAX(position), -1) + 1 FROM `+j.listeners+
				` WHERE org_id = ? AND sidecar_id = ? AND listener_name = ?`,
				orgID, t.SidecarID, t.ListenerName).Scan(&pos).Error
		}
		if err != nil {
			return err
		}
		if mirrorID == "" {
			err = tx.Exec(`INSERT INTO `+j.listeners+` (org_id, `+j.listenerCol+`, sidecar_id, listener_name, position)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, orgID, ruleName, t.SidecarID, t.ListenerName, pos).Error
			if err != nil {
				return err
			}
			continue
		}
		var ruleExists bool
		err = tx.Raw(`SELECT EXISTS (SELECT 1 FROM `+j.rules+` WHERE org_id = ? AND name = ?)`, orgID, ruleName).
			Scan(&ruleExists).Error
		if err != nil {
			return err
		}
		if !ruleExists {
			// The listener table refuses this through its foreign key; the
			// INSERT ... SELECT below would insert nothing and say nothing.
			return ErrNotFound
		}
		err = tx.Exec(`INSERT INTO `+j.mirrors+` (org_id, `+j.mirrorRuleCol+`, connection_id, position)
		SELECT r.org_id, `+j.mirrorRuleValue+`, ?, ? FROM `+j.rules+` r WHERE r.org_id = ? AND r.name = ?
		ON CONFLICT DO NOTHING`, mirrorID, pos, orgID, ruleName).Error
		if err != nil {
			return err
		}
	}
	return nil
}

// moveToMirrorsTx moves the listener bindings of one sidecar whose listener
// has a mirror onto that mirror, with their positions.
func (j sidecarRuleJunction) moveToMirrorsTx(tx *gorm.DB, orgID, sidecarID string) error {
	err := tx.Exec(`
	INSERT INTO `+j.mirrors+` (org_id, `+j.mirrorRuleCol+`, connection_id, position)
	SELECT b.org_id, `+j.mirrorRuleValue+`, c.id, b.position
	FROM `+j.listeners+` b
	JOIN `+j.rules+` r ON r.org_id = b.org_id AND r.name = b.`+j.listenerCol+`
	JOIN private.connections c
	  ON c.org_id = b.org_id AND c.sidecar_id = b.sidecar_id AND c.sidecar_listener = b.listener_name
	WHERE b.org_id = ? AND b.sidecar_id = ?
	ON CONFLICT DO NOTHING`, orgID, sidecarID).Error
	if err != nil {
		return err
	}
	return tx.Exec(`
	DELETE FROM `+j.listeners+` b USING private.connections c
	WHERE c.org_id = b.org_id AND c.sidecar_id = b.sidecar_id AND c.sidecar_listener = b.listener_name
	  AND b.org_id = ? AND b.sidecar_id = ?`, orgID, sidecarID).Error
}

// MoveSidecarBindingsToMirrorsTx moves every rule binding of one sidecar onto
// the mirror of its listener, where the listener has one. The mirror writer
// calls it after each write, so a mirror made after migration 000132 (a
// fallback name, an org that turns beta.sidecar_listeners on) takes the
// bindings of its listener too.
func MoveSidecarBindingsToMirrorsTx(tx *gorm.DB, orgID, sidecarID string) error {
	for _, j := range sidecarRuleJunctions {
		if err := j.moveToMirrorsTx(tx, orgID, sidecarID); err != nil {
			return err
		}
	}
	return nil
}

// isSidecarMirrorTx reports whether a connection mirrors a sidecar listener.
func isSidecarMirrorTx(tx *gorm.DB, orgID, connectionID string) (bool, error) {
	var mirror bool
	err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM private.connections
	WHERE org_id = ? AND id = ? AND sidecar_id IS NOT NULL)`, orgID, connectionID).Scan(&mirror).Error
	return mirror, err
}

// notMirrorConnection is SQL over a connection_id column: true for a
// connection that is not a sidecar mirror. A rule's connection list reads
// and writes only those; the sidecar path owns the rows on mirrors.
const notMirrorConnection = `connection_id NOT IN (SELECT id FROM private.connections WHERE sidecar_id IS NOT NULL)`

// refuseMirrorConnectionsTx refuses a connection list that names a mirror.
func refuseMirrorConnectionsTx(tx *gorm.DB, orgID string, connectionIDs []string) error {
	if len(connectionIDs) == 0 {
		return nil
	}
	var mirror bool
	err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM private.connections
	WHERE org_id = ? AND id::text IN ? AND sidecar_id IS NOT NULL)`, orgID, connectionIDs).Scan(&mirror).Error
	if err != nil {
		return err
	}
	if mirror {
		return ErrSidecarMirrorRuleBinding
	}
	return nil
}

// mirrorRulesUnchangedTx guards a write of one connection's rule list. On a
// mirror it answers true when the list is the one stored, so the caller writes
// nothing, and ErrSidecarMirrorRuleBinding when it differs. On any other
// connection it answers false.
//
// A round trip of a mirror's own list passes: the connection form sends back
// what it read.
func mirrorRulesUnchangedTx(tx *gorm.DB, table, orgID, connectionID string, ruleIDs []string) (bool, error) {
	mirror, err := isSidecarMirrorTx(tx, orgID, connectionID)
	if err != nil || !mirror {
		return false, err
	}
	var stored []string
	err = tx.Raw(`SELECT rule_id::text FROM `+table+` WHERE connection_id = ?`, connectionID).Scan(&stored).Error
	if err != nil {
		return false, err
	}
	want := map[string]bool{}
	for _, id := range ruleIDs {
		want[id] = true
	}
	have := map[string]bool{}
	for _, id := range stored {
		have[id] = true
	}
	if !maps.Equal(want, have) {
		return false, ErrSidecarMirrorRuleBinding
	}
	return true, nil
}
