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

func TestSidecarAccessRequestRules(t *testing.T) {
	startTestDB(t)
	orgID := uuid.MustParse(testOrgID)

	// Every rule writer outside the sidecar path builds a rule without
	// sidecar_names. The field's default is what keeps those writes off the
	// NOT NULL column.
	t.Run("a rule written without sidecar names stores none", func(t *testing.T) {
		rule := newAccessRequestRule(orgID, "prod-command", models.AccessTypeCommand)
		rule.ConnectionNames = []string{"pg-prod"}
		if err := models.CreateAccessRequestRule(models.DB, rule); err != nil {
			t.Fatalf("create rule: %v", err)
		}
		stored, err := models.GetAccessRequestRuleByName(models.DB, rule.Name, orgID)
		if err != nil || len(stored.SidecarNames) != 0 {
			t.Fatalf("expected no sidecar names, got rule=%+v, err=%v", stored, err)
		}
		if err := models.UpdateAccessRequestRule(models.DB, stored); err != nil {
			t.Fatalf("save the loaded rule back: %v", err)
		}
	})

	seedSidecar(t, testOrgID, "sidecar-a")
	rule := newAccessRequestRule(orgID, "prod-approvals", models.AccessTypeSidecar)
	rule.SidecarNames = []string{"sidecar-a"}
	if err := models.CreateAccessRequestRule(models.DB, rule); err != nil {
		t.Fatalf("seed sidecar rule: %v", err)
	}

	t.Run("a sidecar rule stores its sidecar names", func(t *testing.T) {
		stored, err := models.GetAccessRequestRuleByName(models.DB, rule.Name, orgID)
		if err != nil || !slices.Equal(stored.SidecarNames, []string{"sidecar-a"}) {
			t.Fatalf("expected [sidecar-a], got rule=%+v, err=%v", stored, err)
		}
	})

	t.Run("sidecar names resolve inside the organization only", func(t *testing.T) {
		otherOrgID := "00000000-0000-0000-0000-0000000000b2"
		if err := models.DB.Exec(
			`INSERT INTO private.orgs (id, name) VALUES (?, 'other-org')`, otherOrgID).Error; err != nil {
			t.Fatalf("seed org: %v", err)
		}
		seedSidecar(t, otherOrgID, "sidecar-other")
		found, err := models.ListSidecarNames(models.DB, testOrgID, []string{"sidecar-a", "sidecar-other", "missing"})
		if err != nil || !slices.Equal(found, []string{"sidecar-a"}) {
			t.Fatalf("expected [sidecar-a], got %v, err=%v", found, err)
		}
	})

	// Gateway rule matching must not change. A connection named like a sidecar
	// must never pick up a sidecar rule.
	t.Run("connection lookups never match a sidecar rule", func(t *testing.T) {
		for _, accessType := range []string{models.AccessTypeJit, models.AccessTypeCommand, models.AccessTypeJitCommand} {
			_, err := models.GetAccessRequestRuleByResourceNameAndAccessType(models.DB, orgID, "sidecar-a", accessType)
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("access_type %s: expected gorm.ErrRecordNotFound, got %v", accessType, err)
			}
		}
		rules, err := models.GetConnectionAccessRequestRules(models.DB, orgID, "sidecar-a")
		if err != nil || len(rules) != 0 {
			t.Errorf("expected no connection rules, got %d rules, err=%v", len(rules), err)
		}
	})

	// A sidecar rule may carry attributes, which a connection must never match.
	t.Run("attribute lookups never match a sidecar rule", func(t *testing.T) {
		if err := models.UpsertAccessRequestRuleAttributes(models.DB, orgID, rule.Name, []string{"production"}); err != nil {
			t.Fatalf("link attribute: %v", err)
		}
		for _, accessType := range []string{models.AccessTypeJit, models.AccessTypeCommand, models.AccessTypeJitCommand} {
			_, err := models.GetRequestRulesByAttributes(models.DB, orgID, []string{"production"}, accessType)
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("access_type %s: expected gorm.ErrRecordNotFound, got %v", accessType, err)
			}
		}
	})

	// The subtests below each break the check constraint. gorm translates the
	// violation into a sentinel that drops the constraint name, and the
	// embedded backend appends a second error, so errors.Is cannot see the
	// sentinel and its text is matched instead.
	checkViolated := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), gorm.ErrCheckConstraintViolated.Error()) {
			t.Fatalf("expected the check constraint to refuse the write, got %v", err)
		}
	}

	t.Run("a sidecar rule cannot target a connection", func(t *testing.T) {
		bad := newAccessRequestRule(orgID, "sidecar-rule-with-connection", models.AccessTypeSidecar)
		bad.ConnectionNames = []string{"pg-prod"}
		bad.SidecarNames = []string{"sidecar-a"}
		checkViolated(t, models.CreateAccessRequestRule(models.DB, bad))
	})

	t.Run("a connection rule cannot list a sidecar", func(t *testing.T) {
		bad := newAccessRequestRule(orgID, "connection-rule-with-sidecar", models.AccessTypeJit)
		bad.ConnectionNames = []string{"pg-prod"}
		bad.SidecarNames = []string{"sidecar-a"}
		checkViolated(t, models.CreateAccessRequestRule(models.DB, bad))
	})

	// Held by the check, so the MCP rule tools cannot retype one either.
	t.Run("a sidecar rule that lists sidecars cannot change kind", func(t *testing.T) {
		retyped := *rule
		retyped.AccessType = models.AccessTypeJit
		retyped.ConnectionNames = []string{"pg-prod"}
		checkViolated(t, models.UpdateAccessRequestRule(models.DB, &retyped))

		stored, err := models.GetAccessRequestRuleByName(models.DB, rule.Name, orgID)
		if err != nil || stored.AccessType != models.AccessTypeSidecar {
			t.Fatalf("expected the rule to stay a sidecar rule, got rule=%+v, err=%v", stored, err)
		}
	})
}

func seedSidecar(t *testing.T, orgID, name string) {
	t.Helper()
	s := &models.Sidecar{OrgID: orgID, Name: name, KeyHash: "hash-" + orgID + "-" + name, CreatedBy: "test"}
	if err := models.CreateSidecar(models.DB, s); err != nil {
		t.Fatalf("seed sidecar %s: %v", name, err)
	}
}

func newAccessRequestRule(orgID uuid.UUID, name, accessType string) *models.AccessRequestRule {
	return &models.AccessRequestRule{
		OrgID:                  orgID,
		Name:                   name,
		AccessType:             accessType,
		ConnectionNames:        []string{},
		ApprovalRequiredGroups: []string{},
		ReviewersGroups:        []string{"admin"},
		ForceApprovalGroups:    []string{},
		MinApprovals:           ptr.Int(1),
	}
}
