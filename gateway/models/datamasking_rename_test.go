package models_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// ENG-854: a rename wrote the attributes under the new name while the rule
// row kept the old one, so the attribute foreign key refused the write.
func TestUpdateDataMaskingRuleRenamesWithAttributes(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)
	rule := &models.DataMaskingRule{
		ID: uuid.NewString(), OrgID: testOrgID, Name: "old-name", UpdatedAt: time.Now().UTC(),
	}
	if err := models.CreateDataMaskingRuleTx(models.DB, rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if err := models.UpsertDatamaskingRuleAttributes(models.DB, orgID, "old-name", []string{"kept"}); err != nil {
		t.Fatalf("seed attributes: %v", err)
	}

	rule.Name = "new-name"
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := models.UpdateDataMaskingRuleTx(tx, rule); err != nil {
			return err
		}
		return models.UpsertDatamaskingRuleAttributes(tx, orgID, rule.Name, []string{"kept", "added"})
	})
	if err != nil {
		t.Fatalf("rename with attributes: %v", err)
	}

	got, err := models.GetDataMaskingRuleByID(testOrgID, rule.ID)
	if err != nil {
		t.Fatalf("read rule: %v", err)
	}
	if got.Name != "new-name" {
		t.Fatalf("name = %q, want new-name", got.Name)
	}
	var names []string
	if err := models.DB.Raw(`SELECT attribute_name FROM private.datamasking_rules_attributes
		WHERE org_id = ? AND datamasking_rule_name = ? ORDER BY attribute_name`, testOrgID, "new-name").
		Scan(&names).Error; err != nil {
		t.Fatalf("read attributes: %v", err)
	}
	if len(names) != 2 || names[0] != "added" || names[1] != "kept" {
		t.Fatalf("attributes = %v, want [added kept]", names)
	}
}

func TestUpdateDataMaskingRuleRenameConflict(t *testing.T) {
	startTestDB(t)
	for _, name := range []string{"first", "second"} {
		r := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: testOrgID, Name: name, UpdatedAt: time.Now().UTC()}
		if err := models.CreateDataMaskingRuleTx(models.DB, r); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if name == "second" {
			r.Name = "first"
			if err := models.UpdateDataMaskingRuleTx(models.DB, r); err == nil {
				t.Fatal("rename to a taken name: want an error")
			}
		}
	}
}

func TestUpdateDataMaskingRuleRefusesEmptyName(t *testing.T) {
	startTestDB(t)
	r := &models.DataMaskingRule{ID: uuid.NewString(), OrgID: testOrgID, Name: "rule", UpdatedAt: time.Now().UTC()}
	if err := models.CreateDataMaskingRuleTx(models.DB, r); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	r.Name = ""
	if err := models.UpdateDataMaskingRuleTx(models.DB, r); err == nil {
		t.Fatal("update with an empty name: want an error")
	}
}
