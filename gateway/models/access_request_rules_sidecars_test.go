package models_test

import (
	"errors"
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

	rule := newAccessRequestRule(orgID, "prod-approvals", models.AccessTypeSidecar)
	if err := models.CreateAccessRequestRule(models.DB, rule); err != nil {
		t.Fatalf("seed sidecar rule: %v", err)
	}

	// Gateway rule matching must not change. A sidecar rule may carry
	// attributes, which a connection must never match.
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

	// The control-plane handlers write a rule and its attributes in one
	// transaction, so a failed attribute write must take the rule with it.
	t.Run("a failed attribute write rolls back the rule", func(t *testing.T) {
		fresh := newAccessRequestRule(orgID, "rolled-back", models.AccessTypeSidecar)
		tooLong := strings.Repeat("a", 256) // attributes.name is VARCHAR(255)
		err := models.DB.Transaction(func(tx *gorm.DB) error {
			if err := models.CreateAccessRequestRule(tx, fresh); err != nil {
				return err
			}
			return models.UpsertAccessRequestRuleAttributes(tx, orgID, fresh.Name, []string{tooLong})
		})
		if err == nil {
			t.Fatal("expected the oversized attribute name to fail the write")
		}
		if _, err := models.GetAccessRequestRuleByName(models.DB, fresh.Name, orgID); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("expected the rule to roll back, got %v", err)
		}
	})
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
