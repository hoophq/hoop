package services

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProtectionProfileManagedBy is the ownership marker written to every row
// (attributes and rules of all kinds) materialized by the protection profile
// lifecycle. Rows carrying it are read-only through the public API and are
// created/deleted exclusively by ApplyOrgProtectionProfile.
const ProtectionProfileManagedBy = "hoop"

// ErrInvalidProtectionProfile is returned when the supplied profile id is not
// part of the catalog.
var ErrInvalidProtectionProfile = fmt.Errorf("invalid protection profile")

// IsValidProtectionProfile reports whether id names a selectable profile.
// nil/empty is valid and means manual configuration.
func IsValidProtectionProfile(id string) bool {
	_, ok := protectionProfileCatalog[id]
	return ok
}

// IsEnterpriseProtectionProfile reports whether the profile requires an
// enterprise license. Unknown ids return false; validate first.
func IsEnterpriseProtectionProfile(id string) bool {
	spec, ok := protectionProfileCatalog[id]
	return ok && spec.EnterpriseOnly
}

// ProtectionProfileAttributeName returns the managed attribute name for a
// profile id, or nil when the id is nil/unknown (manual configuration).
func ProtectionProfileAttributeName(id *string) *string {
	if id == nil {
		return nil
	}
	if spec, ok := protectionProfileCatalog[*id]; ok {
		return &spec.AttributeName
	}
	return nil
}

// ProtectionProfileApplyResult summarizes an apply operation for tracking.
type ProtectionProfileApplyResult struct {
	PreviousProfile     *string
	ConnectionsAffected int64
}

// ApplyOrgProtectionProfile switches the organization's default protection
// profile in a single transaction.
//
// newProfile == nil selects manual configuration: every managed_by='hoop'
// rule and attribute is deleted (junctions cascade) and the org column is
// set to NULL.
//
// newProfile == &id materializes the profile: the profile attribute and every
// rule the profile references are created if absent (never rewritten), rules
// are tagged to the attribute via the junction tables, connections previously
// carrying the old profile attribute (or all connections, when no profile was
// active) are tagged with the new attribute, and managed rules the new profile
// does not reference are garbage-collected together with the old attribute.
//
// adminGroup is used as the reviewer/approver group on materialized access
// request rules; when empty it defaults to types.GroupAdmin.
func ApplyOrgProtectionProfile(ctx context.Context, orgID uuid.UUID, newProfile *string, adminGroup string) (*ProtectionProfileApplyResult, error) {
	if newProfile != nil && !IsValidProtectionProfile(*newProfile) {
		return nil, ErrInvalidProtectionProfile
	}
	if adminGroup == "" {
		adminGroup = types.GroupAdmin
	}

	result := &ProtectionProfileApplyResult{}
	err := models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		org, err := lockOrgProfile(tx, orgID)
		if err != nil {
			return err
		}
		result.PreviousProfile = org.DefaultProtectionProfile

		if newProfile == nil {
			affected, err := deleteAllManagedRows(tx, orgID)
			if err != nil {
				return err
			}
			result.ConnectionsAffected = affected
			return models.UpdateOrgDefaultProtectionProfile(tx, orgID.String(), nil)
		}

		spec := protectionProfileCatalog[*newProfile]
		if err := materializeProfile(tx, orgID, spec, adminGroup); err != nil {
			return err
		}

		// Tagging scope depends on the previous state:
		//   * no profile active   -> tag every connection (first selection)
		//   * other profile active -> move that profile's connections over
		//   * same profile active  -> junction no-op (manual detachments stay)
		var oldAttr string
		firstSelection := org.DefaultProtectionProfile == nil
		if prev := org.DefaultProtectionProfile; prev != nil && *prev != *newProfile {
			if prevSpec, ok := protectionProfileCatalog[*prev]; ok {
				oldAttr = prevSpec.AttributeName
			}
		}
		if firstSelection || oldAttr != "" {
			affected, err := tagConnections(tx, orgID, spec.AttributeName, oldAttr)
			if err != nil {
				return err
			}
			result.ConnectionsAffected = affected
		}

		if err := garbageCollect(tx, orgID, spec, oldAttr); err != nil {
			return err
		}
		return models.UpdateOrgDefaultProtectionProfile(tx, orgID.String(), newProfile)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// lockOrgProfile reads the org row FOR UPDATE so concurrent profile switches
// for the same org serialize instead of interleaving their create/delete sets.
func lockOrgProfile(tx *gorm.DB, orgID uuid.UUID) (*models.Organization, error) {
	var org models.Organization
	err := tx.Raw(`SELECT id, default_protection_profile FROM private.orgs WHERE id = ? FOR UPDATE`, orgID).
		First(&org).Error
	if err == gorm.ErrRecordNotFound {
		return nil, models.ErrNotFound
	}
	return &org, err
}

// TagConnectionWithActiveProfile attaches the org's active protection profile
// attribute to a newly created connection. No-op when the org has no profile
// selected (manual configuration). Errors are returned for the caller to log;
// they must not abort connection creation.
func TagConnectionWithActiveProfile(ctx context.Context, orgID, connectionName string) error {
	var profile *string
	err := models.DB.WithContext(ctx).
		Raw(`SELECT default_protection_profile FROM private.orgs WHERE id = ?`, orgID).
		Scan(&profile).Error
	if err != nil || profile == nil {
		return err
	}
	spec, ok := protectionProfileCatalog[*profile]
	if !ok {
		return fmt.Errorf("org %s references unknown protection profile %q", orgID, *profile)
	}
	return models.DB.WithContext(ctx).Exec(`
		INSERT INTO private.connections_attributes (org_id, connection_name, attribute_name)
		SELECT ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM private.attributes WHERE org_id = ? AND name = ?)
		ON CONFLICT DO NOTHING`,
		orgID, connectionName, spec.AttributeName, orgID, spec.AttributeName).Error
}

// materializeProfile creates the profile attribute and every referenced rule
// when absent, and tags each rule to the attribute. Existing rows are reused
// untouched, so re-applying is idempotent and rule content is never rewritten.
func materializeProfile(tx *gorm.DB, orgID uuid.UUID, spec protectionProfileSpec, adminGroup string) error {
	managed := ProtectionProfileManagedBy
	desc := "Managed by Hoop. Applied by the " + spec.DisplayName + " protection profile."
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.Attribute{
		OrgID:       orgID,
		Name:        spec.AttributeName,
		Description: &desc,
		ManagedBy:   &managed,
	}).Error; err != nil {
		return fmt.Errorf("failed creating profile attribute: %w", err)
	}

	for _, name := range spec.Guardrails {
		gr := protectionGuardrailCatalog[name]
		var input, output map[string]any
		if err := json.Unmarshal([]byte(gr.InputJSON), &input); err != nil {
			return fmt.Errorf("malformed guardrail catalog input %q: %w", name, err)
		}
		if err := json.Unmarshal([]byte(gr.OutputJSON), &output); err != nil {
			return fmt.Errorf("malformed guardrail catalog output %q: %w", name, err)
		}
		err := tx.Table("private.guardrail_rules").Clauses(clause.OnConflict{DoNothing: true}).
			Create(&models.GuardRailRules{
				ID:          uuid.NewString(),
				OrgID:       orgID.String(),
				Name:        gr.Name,
				Description: gr.Description,
				Input:       input,
				Output:      output,
				ManagedBy:   &managed,
			}).Error
		if err != nil {
			return fmt.Errorf("failed creating guardrail rule %q: %w", name, err)
		}
		if err := linkAttr(tx, &models.GuardrailRuleAttribute{
			OrgID: orgID, AttributeName: spec.AttributeName, GuardrailRuleName: gr.Name,
		}); err != nil {
			return err
		}
	}

	for _, name := range spec.Masking {
		dm := protectionMaskingCatalog[name]
		var supported models.SupportedEntityTypesList
		var custom models.CustomEntityTypesList
		if err := json.Unmarshal([]byte(dm.SupportedEntityTypes), &supported); err != nil {
			return fmt.Errorf("malformed masking catalog entity types %q: %w", name, err)
		}
		if err := json.Unmarshal([]byte(dm.CustomEntityTypes), &custom); err != nil {
			return fmt.Errorf("malformed masking catalog custom types %q: %w", name, err)
		}
		threshold := dm.ScoreThreshold
		err := tx.Table("private.datamasking_rules").Clauses(clause.OnConflict{DoNothing: true}).
			Create(&models.DataMaskingRule{
				ID:                   uuid.NewString(),
				OrgID:                orgID.String(),
				Name:                 dm.Name,
				Description:          dm.Description,
				SupportedEntityTypes: supported,
				CustomEntityTypes:    custom,
				ScoreThreshold:       &threshold,
				ManagedBy:            &managed,
			}).Error
		if err != nil {
			return fmt.Errorf("failed creating data masking rule %q: %w", name, err)
		}
		if err := linkAttr(tx, &models.DatamaskingRuleAttribute{
			OrgID: orgID, AttributeName: spec.AttributeName, DatamaskingRuleName: dm.Name,
		}); err != nil {
			return err
		}
	}

	for _, name := range spec.AccessRules {
		ar := protectionAccessRuleCatalog[name]
		rule := &models.AccessRequestRule{
			ID:                     uuid.New(),
			OrgID:                  orgID,
			Name:                   ar.Name,
			Description:            &ar.Description,
			AccessType:             ar.AccessType,
			ManagedBy:              &managed,
			ConnectionNames:        pq.StringArray{},
			ApprovalRequiredGroups: pq.StringArray{adminGroup},
			ReviewersGroups:        pq.StringArray{adminGroup},
			ForceApprovalGroups:    pq.StringArray{adminGroup},
			MinApprovals:           &ar.MinApprovals,
		}
		if ar.AccessMaxDuration > 0 {
			rule.AccessMaxDuration = &ar.AccessMaxDuration
		}
		err := tx.Omit("RuleAttributes").Clauses(clause.OnConflict{DoNothing: true}).Create(rule).Error
		if err != nil {
			return fmt.Errorf("failed creating access request rule %q: %w", name, err)
		}
		if err := linkAttr(tx, &models.AccessRequestRuleAttribute{
			OrgID: orgID, AttributeName: spec.AttributeName, AccessRuleName: ar.Name,
		}); err != nil {
			return err
		}
	}

	for _, name := range spec.Analyzers {
		an := protectionAnalyzerCatalog[name]
		var risk models.AISessionAnalyzerRiskEvaluation
		if err := json.Unmarshal([]byte(an.RiskEvaluationJSON), &risk); err != nil {
			return fmt.Errorf("malformed analyzer catalog risk evaluation %q: %w", name, err)
		}
		err := tx.Omit("RuleAttributes").Clauses(clause.OnConflict{DoNothing: true}).
			Create(&models.AISessionAnalyzerRules{
				ID:              uuid.New(),
				OrgID:           orgID,
				Name:            an.Name,
				Description:     &an.Description,
				ConnectionNames: pq.StringArray{},
				RiskEvaluation:  risk,
				ManagedBy:       &managed,
			}).Error
		if err != nil {
			return fmt.Errorf("failed creating analyzer rule %q: %w", name, err)
		}
		if err := linkAttr(tx, &models.AISessionAnalyzerRuleAttribute{
			OrgID: orgID, AttributeName: spec.AttributeName, AnalyzerRuleName: an.Name,
		}); err != nil {
			return err
		}
	}
	return nil
}

func linkAttr(tx *gorm.DB, junction any) error {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(junction).Error; err != nil {
		return fmt.Errorf("failed tagging rule with profile attribute: %w", err)
	}
	return nil
}

// tagConnections attaches the new profile attribute to connections. When an
// old profile attribute is supplied, only connections carrying it are tagged
// (manual detachments are respected); otherwise every connection in the org
// is tagged (first selection). Returns the number of connections now tagged.
func tagConnections(tx *gorm.DB, orgID uuid.UUID, newAttr, oldAttr string) (int64, error) {
	var res *gorm.DB
	if oldAttr != "" {
		res = tx.Exec(`
			INSERT INTO private.connections_attributes (org_id, connection_name, attribute_name)
			SELECT ca.org_id, ca.connection_name, ?
			FROM private.connections_attributes ca
			WHERE ca.org_id = ? AND ca.attribute_name = ?
			ON CONFLICT DO NOTHING`, newAttr, orgID, oldAttr)
	} else {
		res = tx.Exec(`
			INSERT INTO private.connections_attributes (org_id, connection_name, attribute_name)
			SELECT c.org_id, c.name, ?
			FROM private.connections c
			WHERE c.org_id = ?
			ON CONFLICT DO NOTHING`, newAttr, orgID)
	}
	if res.Error != nil {
		return 0, fmt.Errorf("failed tagging connections with profile attribute: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// garbageCollect removes the previous profile attribute (its connection and
// rule junctions cascade) and every managed rule the new profile does not
// reference. Rules shared between the old and new profile survive untouched.
func garbageCollect(tx *gorm.DB, orgID uuid.UUID, spec protectionProfileSpec, oldAttr string) error {
	if oldAttr != "" {
		if err := tx.Exec(`DELETE FROM private.attributes WHERE org_id = ? AND name = ? AND managed_by = ?`,
			orgID, oldAttr, ProtectionProfileManagedBy).Error; err != nil {
			return fmt.Errorf("failed removing previous profile attribute: %w", err)
		}
	}
	for table, keep := range map[string][]string{
		"private.guardrail_rules":           spec.Guardrails,
		"private.datamasking_rules":         spec.Masking,
		"private.access_request_rules":      spec.AccessRules,
		"private.ai_session_analyzer_rules": spec.Analyzers,
	} {
		q := tx.Table(table).Where("org_id = ? AND managed_by = ?", orgID, ProtectionProfileManagedBy)
		if len(keep) > 0 {
			q = q.Where("name NOT IN ?", keep)
		}
		if err := q.Delete(nil).Error; err != nil {
			return fmt.Errorf("failed garbage-collecting managed rules in %s: %w", table, err)
		}
	}
	return nil
}

// deleteAllManagedRows removes every managed rule and attribute for the org
// (manual configuration selected). Junction rows cascade via FKs. Returns the
// number of connections that lost the profile attribute.
func deleteAllManagedRows(tx *gorm.DB, orgID uuid.UUID) (int64, error) {
	var affected int64
	err := tx.Raw(`
		SELECT COUNT(*) FROM private.connections_attributes ca
		JOIN private.attributes a ON a.org_id = ca.org_id AND a.name = ca.attribute_name
		WHERE ca.org_id = ? AND a.managed_by = ?`, orgID, ProtectionProfileManagedBy).
		Scan(&affected).Error
	if err != nil {
		return 0, err
	}
	for _, table := range []string{
		"private.guardrail_rules",
		"private.datamasking_rules",
		"private.access_request_rules",
		"private.ai_session_analyzer_rules",
		"private.attributes",
	} {
		if err := tx.Table(table).
			Where("org_id = ? AND managed_by = ?", orgID, ProtectionProfileManagedBy).
			Delete(nil).Error; err != nil {
			return 0, fmt.Errorf("failed deleting managed rows in %s: %w", table, err)
		}
	}
	return affected, nil
}
