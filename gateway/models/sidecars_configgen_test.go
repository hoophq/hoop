package models_test

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// One embedded database for every case: each boot costs tens of seconds, and
// the package runs under make test-oss's 15 minute timeout.
func TestTheSidecarConfigGen(t *testing.T) {
	startTestDB(t)
	t.Run("follows every compose input", configGenFollowsEveryComposeInput)
	t.Run("moves once per transaction", configGenMovesOncePerTransaction)
	t.Run("handshake records the composed gen", handshakeRecordsTheComposedGen)
}

// Every write a served document is composed from moves the org gen, and the
// per-minute handshake UPDATE does not. A table missing its trigger fails
// here instead of serving a stale document as 304.
func configGenFollowsEveryComposeInput(t *testing.T) {
	keyHash := models.HashAPIKey("hsc_config_gen_test")
	sc := &models.Sidecar{OrgID: testOrgID, Name: "config-gen", KeyHash: keyHash, CreatedBy: "tests@hoop.dev"}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	gen := func() int64 {
		t.Helper()
		got, err := models.GetSidecarByKeyHash(models.DB, keyHash)
		if err != nil {
			t.Fatalf("read sidecar: %v", err)
		}
		return got.OrgConfigGen
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if err := models.DB.Exec(sql, args...).Error; err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	for _, w := range []struct {
		name  string
		write func()
	}{
		{"sidecar configuration", func() {
			exec(`UPDATE private.sidecars SET configuration = '{"log_level":"debug"}' WHERE id = ?`, sc.ID)
		}},
		{"guardrail rule", func() {
			exec(`INSERT INTO private.guardrail_rules (org_id, name) VALUES (?, 'g1')`, testOrgID)
		}},
		{"guardrail binding", func() {
			exec(`INSERT INTO private.guardrail_rules_listeners (org_id, guardrail_rule_name, sidecar_id, listener_name)
				VALUES (?, 'g1', ?, 'appdb')`, testOrgID, sc.ID)
		}},
		{"data masking rule", func() {
			exec(`INSERT INTO private.datamasking_rules (org_id, name) VALUES (?, 'm1')`, testOrgID)
		}},
		{"data masking binding", func() {
			exec(`INSERT INTO private.datamasking_rules_listeners (org_id, datamasking_rule_name, sidecar_id, listener_name)
				VALUES (?, 'm1', ?, 'appdb')`, testOrgID, sc.ID)
		}},
		{"analyzer rule", func() {
			exec(`INSERT INTO private.ai_session_analyzer_rules (org_id, name, connection_names, risk_evaluation)
				VALUES (?, 'a1', '{}', '{}')`, testOrgID)
		}},
		{"analyzer binding", func() {
			exec(`INSERT INTO private.ai_session_analyzer_rules_listeners (org_id, analyzer_rule_name, sidecar_id, listener_name)
				VALUES (?, 'a1', ?, 'appdb')`, testOrgID, sc.ID)
		}},
		{"binding delete", func() {
			exec(`DELETE FROM private.guardrail_rules_listeners WHERE sidecar_id = ?`, sc.ID)
		}},
		{"license", func() {
			exec(`UPDATE private.orgs SET license_data = '{"payload":{}}' WHERE id = ?`, testOrgID)
		}},
	} {
		before := gen()
		w.write()
		if after := gen(); after <= before {
			t.Errorf("a %s write left the gen at %d", w.name, after)
		}
	}

	before := gen()
	if err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.2.3", "", "", "", "rev-1", nil, &before); err != nil {
		t.Fatalf("record handshake: %v", err)
	}
	exec(`UPDATE private.sidecars SET configuration = configuration WHERE id = ?`, sc.ID)
	exec(`UPDATE private.orgs SET license_data = license_data WHERE id = ?`, testOrgID)
	if after := gen(); after != before {
		t.Errorf("writes that changed no compose input moved the gen from %d to %d", before, after)
	}
}

// A transaction moves the gen once, however many rows it writes: a rule
// bound to hundreds of listeners must not write the gen row hundreds of times.
func configGenMovesOncePerTransaction(t *testing.T) {
	keyHash := models.HashAPIKey("hsc_config_gen_once_test")
	sc := &models.Sidecar{OrgID: testOrgID, Name: "config-gen-once", KeyHash: keyHash, CreatedBy: "tests@hoop.dev"}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	gen := func() int64 {
		t.Helper()
		got, err := models.GetSidecarByKeyHash(models.DB, keyHash)
		if err != nil {
			t.Fatalf("read sidecar: %v", err)
		}
		return got.OrgConfigGen
	}

	before := gen()
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO private.guardrail_rules (org_id, name) VALUES (?, 'bulk')`, testOrgID).Error; err != nil {
			return err
		}
		for _, l := range []string{"a", "b", "c"} {
			if err := tx.Exec(`INSERT INTO private.guardrail_rules_listeners (org_id, guardrail_rule_name, sidecar_id, listener_name)
				VALUES (?, 'bulk', ?, ?)`, testOrgID, sc.ID, l).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("bulk write: %v", err)
	}
	if after := gen(); after != before+1 {
		t.Errorf("one transaction moved the gen from %d to %d, want %d", before, after, before+1)
	}
}

// A composed handshake stores the gen and the time; a skipped one keeps them.
func handshakeRecordsTheComposedGen(t *testing.T) {
	keyHash := models.HashAPIKey("hsc_composed_gen_test")
	sc := &models.Sidecar{OrgID: testOrgID, Name: "composed-gen", KeyHash: keyHash, CreatedBy: "tests@hoop.dev"}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	gen := int64(7)
	if err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.2.3", "", "", "", "rev-1", nil, &gen); err != nil {
		t.Fatalf("record composed: %v", err)
	}
	composed, err := models.GetSidecarByKeyHash(models.DB, keyHash)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if composed.ServedGen == nil || *composed.ServedGen != 7 || composed.ComposedAt == nil {
		t.Fatalf("served_gen = %v, composed_at = %v; want 7 and a time", composed.ServedGen, composed.ComposedAt)
	}

	if err := models.RecordSidecarHandshake(models.DB, sc.ID, "1.2.3", "rev-1", "unchanged", "", "rev-1", nil, nil); err != nil {
		t.Fatalf("record skipped: %v", err)
	}
	skipped, err := models.GetSidecarByKeyHash(models.DB, keyHash)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if skipped.ServedGen == nil || *skipped.ServedGen != 7 || !skipped.ComposedAt.Equal(*composed.ComposedAt) {
		t.Errorf("a skipped compose moved served_gen to %v or composed_at to %v", skipped.ServedGen, skipped.ComposedAt)
	}
	if skipped.ServedRevision == nil || *skipped.ServedRevision != "rev-1" {
		t.Errorf("served_revision = %v, want rev-1", skipped.ServedRevision)
	}
}
