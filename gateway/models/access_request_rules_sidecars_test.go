package models_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

func TestAccessRequestRuleSidecars(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)

	sidecarA := seedSidecar(t, testOrgID, "sidecar-a")
	seedSidecar(t, testOrgID, "sidecar-b")
	rule := newSidecarRule(orgID, "prod-approvals")
	if err := models.CreateAccessRequestRule(models.DB, rule); err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	t.Run("lists the sidecars sorted, a duplicate once", func(t *testing.T) {
		err := models.SetAccessRequestRuleSidecars(models.DB, orgID, rule.Name,
			[]string{"sidecar-b", "sidecar-a", "sidecar-b"})
		if err != nil {
			t.Fatalf("set sidecars: %v", err)
		}
		assertRuleSidecars(t, orgID, rule.Name, "sidecar-a", "sidecar-b")
	})

	t.Run("an unknown sidecar changes nothing", func(t *testing.T) {
		err := models.SetAccessRequestRuleSidecars(models.DB, orgID, rule.Name, []string{"sidecar-a", "missing"})
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("expected gorm.ErrRecordNotFound, got %v", err)
		}
		assertRuleSidecars(t, orgID, rule.Name, "sidecar-a", "sidecar-b")
	})

	t.Run("the sidecar of another organization does not resolve", func(t *testing.T) {
		otherOrgID := "00000000-0000-0000-0000-0000000000b2"
		if err := models.DB.Exec(
			`INSERT INTO private.orgs (id, name) VALUES (?, 'other-org')`, otherOrgID).Error; err != nil {
			t.Fatalf("seed org: %v", err)
		}
		seedSidecar(t, otherOrgID, "sidecar-other")
		err := models.SetAccessRequestRuleSidecars(models.DB, orgID, rule.Name, []string{"sidecar-other"})
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("expected gorm.ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("a renamed rule keeps its sidecars", func(t *testing.T) {
		rule.Name = "prod-approvals-renamed"
		if err := models.UpdateAccessRequestRule(models.DB, rule); err != nil {
			t.Fatalf("rename rule: %v", err)
		}
		assertRuleSidecars(t, orgID, rule.Name, "sidecar-a", "sidecar-b")
	})

	t.Run("a deleted sidecar leaves the rule", func(t *testing.T) {
		if err := models.DB.Exec(`DELETE FROM private.sidecars WHERE id = ?`, sidecarA.ID).Error; err != nil {
			t.Fatalf("delete sidecar: %v", err)
		}
		assertRuleSidecars(t, orgID, rule.Name, "sidecar-b")
	})

	// The handler writes a rule and its sidecars in one transaction, so the
	// transaction inside SetAccessRequestRuleSidecars runs as a savepoint.
	t.Run("a rename and a new sidecar list commit together", func(t *testing.T) {
		seedSidecar(t, testOrgID, "sidecar-c")
		oldName := rule.Name
		err := models.DB.Transaction(func(tx *gorm.DB) error {
			rule.Name = "prod-approvals-final"
			if err := models.UpdateAccessRequestRule(tx, rule); err != nil {
				return err
			}
			return models.SetAccessRequestRuleSidecars(tx, orgID, rule.Name, []string{"sidecar-c"})
		})
		if err != nil {
			t.Fatalf("rename and replace sidecars: %v", err)
		}
		assertRuleSidecars(t, orgID, rule.Name, "sidecar-c")
		assertRuleSidecars(t, orgID, oldName)
	})

	t.Run("an unknown sidecar rolls back a new rule", func(t *testing.T) {
		fresh := newSidecarRule(orgID, "rolled-back")
		err := models.DB.Transaction(func(tx *gorm.DB) error {
			if err := models.CreateAccessRequestRule(tx, fresh); err != nil {
				return err
			}
			return models.SetAccessRequestRuleSidecars(tx, orgID, fresh.Name, []string{"missing"})
		})
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("expected gorm.ErrRecordNotFound, got %v", err)
		}
		if _, err := models.GetAccessRequestRuleByName(models.DB, fresh.Name, orgID); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("expected the rule to roll back, got %v", err)
		}
	})

	// gorm translates a constraint violation into a sentinel that drops the
	// constraint name. The embedded backend then appends a second error, so
	// errors.Is cannot see the sentinel and the text is matched instead.
	t.Run("a sidecar rule cannot target a connection", func(t *testing.T) {
		bad := newSidecarRule(orgID, "sidecar-rule-with-connection")
		bad.ConnectionNames = []string{"pg-prod"}
		err := models.CreateAccessRequestRule(models.DB, bad)
		if err == nil || !strings.Contains(err.Error(), gorm.ErrCheckConstraintViolated.Error()) {
			t.Fatalf("expected the check constraint to refuse connection_names on a sidecar rule, got %v", err)
		}
	})

	// Held by the join table, so the MCP rule tools cannot retype one either.
	t.Run("a sidecar rule that lists sidecars cannot change kind", func(t *testing.T) {
		seedSidecar(t, testOrgID, "sidecar-d")
		linked := newSidecarRule(orgID, "linked")
		if err := models.CreateAccessRequestRule(models.DB, linked); err != nil {
			t.Fatalf("seed rule: %v", err)
		}
		if err := models.SetAccessRequestRuleSidecars(models.DB, orgID, linked.Name, []string{"sidecar-d"}); err != nil {
			t.Fatalf("set sidecars: %v", err)
		}

		linked.AccessType = models.AccessTypeJit
		linked.ConnectionNames = []string{"pg-prod"}
		err := models.UpdateAccessRequestRule(models.DB, linked)
		if err == nil || !strings.Contains(err.Error(), gorm.ErrCheckConstraintViolated.Error()) {
			t.Fatalf("expected the join table to refuse the retype, got %v", err)
		}
		stored, err := models.GetAccessRequestRuleByName(models.DB, linked.Name, orgID)
		if err != nil || stored.AccessType != models.AccessTypeSidecar {
			t.Fatalf("expected the rule to stay a sidecar rule, got %+v, err=%v", stored, err)
		}
		assertRuleSidecars(t, orgID, linked.Name, "sidecar-d")
	})

	t.Run("a connection rule cannot list a sidecar", func(t *testing.T) {
		jit := newSidecarRule(orgID, "jit-rule")
		jit.AccessType = models.AccessTypeJit
		jit.ConnectionNames = []string{"pg-prod"}
		if err := models.CreateAccessRequestRule(models.DB, jit); err != nil {
			t.Fatalf("seed rule: %v", err)
		}
		err := models.SetAccessRequestRuleSidecars(models.DB, orgID, jit.Name, []string{"sidecar-d"})
		if err == nil || !strings.Contains(err.Error(), gorm.ErrForeignKeyViolated.Error()) {
			t.Fatalf("expected the foreign key to refuse a sidecar on a connection rule, got %v", err)
		}
	})

	// Gateway rule matching must not change. A connection named like a sidecar
	// must never pick up a sidecar rule.
	t.Run("connection lookups never match a sidecar rule", func(t *testing.T) {
		for _, accessType := range []string{models.AccessTypeJit, models.AccessTypeCommand, models.AccessTypeJitCommand} {
			_, err := models.GetAccessRequestRuleByResourceNameAndAccessType(models.DB, orgID, "sidecar-b", accessType)
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("access_type %s: expected gorm.ErrRecordNotFound, got %v", accessType, err)
			}
		}
		rules, err := models.GetConnectionAccessRequestRules(models.DB, orgID, "sidecar-b")
		if err != nil || len(rules) != 0 {
			t.Errorf("expected no connection rules, got %d rules, err=%v", len(rules), err)
		}
	})
}

func seedSidecar(t *testing.T, orgID, name string) *models.Sidecar {
	t.Helper()
	s := &models.Sidecar{OrgID: orgID, Name: name, KeyHash: "hash-" + orgID + "-" + name, CreatedBy: "test"}
	if err := models.CreateSidecar(models.DB, s); err != nil {
		t.Fatalf("seed sidecar %s: %v", name, err)
	}
	return s
}

func newSidecarRule(orgID uuid.UUID, name string) *models.AccessRequestRule {
	return &models.AccessRequestRule{
		OrgID:                  orgID,
		Name:                   name,
		AccessType:             models.AccessTypeSidecar,
		ConnectionNames:        []string{},
		ApprovalRequiredGroups: []string{},
		ReviewersGroups:        []string{"admin"},
		ForceApprovalGroups:    []string{},
		MinApprovals:           ptr.Int(1),
	}
}

func assertRuleSidecars(t *testing.T, orgID uuid.UUID, ruleName string, want ...string) {
	t.Helper()
	got, err := models.ListAccessRequestRuleSidecarNames(models.DB, orgID, []string{ruleName})
	if err != nil {
		t.Fatalf("list sidecars: %v", err)
	}
	if !slices.Equal(got[ruleName], want) {
		t.Errorf("expected sidecars %v, got %v", want, got[ruleName])
	}
}
