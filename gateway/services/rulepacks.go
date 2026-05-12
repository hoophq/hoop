package services

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const RulepackAttributeNamePrefix = "rulepack_"

func RulepackIDToNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func RulepackIDFromNullString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func RulepackIDToUUIDPointer(s *string) *uuid.UUID {
	if s == nil || *s == "" {
		return nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil
	}
	return &id
}

func RulepackIDFromUUIDPointer(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

const RulepackFlagName = "experimental.rulepacks"

var (
	ErrRulepackInvalidID          = errors.New("invalid rulepack_id")
	ErrRulepackNotFound           = errors.New("rulepack_id does not reference an existing rulepack")
	ErrRulepackIsManaged          = errors.New("managed rulepacks cannot be referenced by user-managed features")
	ErrRulepackInvalidDisplayName = errors.New("rulepack display_name cannot be converted to a valid attribute name (empty or contains only unsupported characters)")
)

const rulepackAttributeNameMaxLen = 255 - len(RulepackAttributeNamePrefix)

// rulepackAttributeNameFromDisplayName derives the attribute name from a rulepack's
// display name. Lowercases, replaces any character outside [a-z0-9] with `_`, collapses
// repeated underscores, trims leading/trailing underscores, prepends the `rulepack_`
// prefix, and truncates to fit private.attributes.name (VARCHAR(255)).
//
// Returns the empty string if the slugged display name is empty (e.g., display_name was
// composed entirely of unsupported characters), letting the caller emit a clear error.
func rulepackAttributeNameFromDisplayName(displayName string) string {
	var b strings.Builder
	b.Grow(len(displayName))
	prevUnderscore := false
	for _, r := range strings.ToLower(displayName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	slug := strings.TrimRight(b.String(), "_")
	if slug == "" {
		return ""
	}
	if len(slug) > rulepackAttributeNameMaxLen {
		slug = strings.TrimRight(slug[:rulepackAttributeNameMaxLen], "_")
	}
	return RulepackAttributeNamePrefix + slug
}

// AssertRulepackUsable validates a rulepack_id provided by an API caller.
// Returns nil if the input is empty (rulepack_id not set), or if the referenced
// rulepack exists and is not managed. Skips all checks when the experimental
// rulepack feature flag is disabled for the org.
func AssertRulepackUsable(orgID string, rulepackIDStr *string) error {
	if rulepackIDStr == nil || *rulepackIDStr == "" {
		return nil
	}
	if !featureflag.IsEnabled(orgID, RulepackFlagName) {
		return nil
	}
	id, err := uuid.Parse(*rulepackIDStr)
	if err != nil {
		return ErrRulepackInvalidID
	}
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		return ErrRulepackInvalidID
	}
	rp, err := models.GetRulepack(models.DB, orgUUID, id)
	if err != nil {
		return ErrRulepackNotFound
	}
	if rp.IsManaged {
		return ErrRulepackIsManaged
	}
	return nil
}

// RulepackRulesInput groups the three nested rule kinds that a rulepack can carry.
// On create, each non-empty slice is inserted; an empty slice skips that kind.
// On update, each slice fully replaces the existing rulepack-owned rules of that kind
// (an empty slice deletes all of them).
type RulepackRulesInput struct {
	DataMaskingRules       []openapi.DataMaskingRuleRequest
	GuardRailRules         []openapi.GuardRailRuleRequest
	AISessionAnalyzerRules []openapi.AISessionAnalyzerRuleRequest
}

// CreateRulepackWithRules creates a rulepack, one rulepack-owned attribute, and any
// nested rules (data masking, guardrails, AI session analyzer) in a single transaction.
//
// The attribute name is derived from the rulepack's display_name (lowercased,
// slug-cased, prepended with `rulepack_`); the attribute description is copied from
// the rulepack's description. Every nested data masking and guardrail rule is also
// tagged with the rulepack attribute via its respective junction so that the rule
// matches connections sharing that attribute at session time. AI session analyzer
// rules have no attribute junction; they are linked only via rulepack_id.
//
// Returns ErrRulepackInvalidDisplayName when the display_name slugs to empty.
// Returns models.ErrAlreadyExists when the rulepack's display_name, the derived
// attribute name, or any nested rule name collides with an existing row in the org.
//
// is_managed is passed through unchanged from the input rulepack; callers (e.g. HTTP
// handler) are responsible for forcing it to false when needed.
func CreateRulepackWithRules(
	ctx context.Context,
	rp *models.Rulepack,
	rules RulepackRulesInput,
) (*models.Rulepack, *models.Attribute, error) {
	attrName := rulepackAttributeNameFromDisplayName(rp.DisplayName)
	if attrName == "" {
		return nil, nil, ErrRulepackInvalidDisplayName
	}

	var attr *models.Attribute
	err := models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := models.CreateRulepack(tx, rp); err != nil {
			return err
		}
		rulepackID := rp.ID
		attr = &models.Attribute{
			OrgID:       rp.OrgID,
			Name:        attrName,
			Description: rp.Description,
			RulepackID:  &rulepackID,
		}
		if err := tx.Create(attr).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return models.ErrAlreadyExists
			}
			return err
		}
		return insertRulepackRulesTx(tx, rp, attrName, rules)
	})
	if err != nil {
		return nil, nil, err
	}
	return rp, attr, nil
}

// UpdateRulepackWithRules updates a rulepack, syncs the derived attribute when
// display_name changes, and FULLY REPLACES the rulepack's nested rules (data masking,
// guardrails, AI session analyzer) with the supplied lists. All writes occur in a
// single transaction.
//
// Pass empty slices to delete all nested rules of that type. Pass the existing list
// unchanged to leave them intact at the row level (existing rows are deleted then
// re-inserted, so this is NOT idempotent at the row-ID level — IDs change on every
// PUT).
//
// Attribute rename behavior is unchanged from previous versions: when the new derived
// slug differs from the old one, the matching attribute is renamed and its description
// refreshed; junction tables follow via ON UPDATE CASCADE.
//
// Returns ErrRulepackInvalidDisplayName when display_name slugs to empty,
// models.ErrNotFound if the rulepack does not exist, and models.ErrAlreadyExists when
// display_name, derived attribute name, or any nested rule name collides.
//
// is_managed is not checked at this layer; HTTP handlers are responsible.
func UpdateRulepackWithRules(
	ctx context.Context,
	rp *models.Rulepack,
	rules RulepackRulesInput,
) (*models.Rulepack, *models.Attribute, error) {
	newAttrName := rulepackAttributeNameFromDisplayName(rp.DisplayName)
	if newAttrName == "" {
		return nil, nil, ErrRulepackInvalidDisplayName
	}

	var renamedAttr *models.Attribute
	err := models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := models.GetRulepack(tx, rp.OrgID, rp.ID)
		if err != nil {
			return err
		}
		oldAttrName := rulepackAttributeNameFromDisplayName(existing.DisplayName)

		if err := models.UpdateRulepack(tx, rp); err != nil {
			return err
		}

		if oldAttrName != newAttrName {
			result := tx.Model(&models.Attribute{}).
				Where("org_id = ? AND rulepack_id = ? AND name = ?", rp.OrgID, rp.ID, oldAttrName).
				Updates(map[string]any{
					"name":        newAttrName,
					"description": rp.Description,
				})
			if result.Error != nil {
				if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
					return models.ErrAlreadyExists
				}
				return result.Error
			}
			if result.RowsAffected > 0 {
				var fetched models.Attribute
				if err := tx.Where("org_id = ? AND name = ?", rp.OrgID, newAttrName).First(&fetched).Error; err != nil {
					return err
				}
				renamedAttr = &fetched
			}
		}

		if err := models.DeleteDataMaskingRulesByRulepackIDTx(tx, rp.OrgID, rp.ID); err != nil {
			return err
		}
		if err := models.DeleteGuardRailRulesByRulepackIDTx(tx, rp.OrgID, rp.ID); err != nil {
			return err
		}
		if err := models.DeleteAISessionAnalyzerRulesByRulepackIDTx(tx, rp.OrgID, rp.ID); err != nil {
			return err
		}

		return insertRulepackRulesTx(tx, rp, newAttrName, rules)
	})
	if err != nil {
		return nil, nil, err
	}
	return rp, renamedAttr, nil
}

// insertRulepackRulesTx inserts each nested rule, tags data masking and guardrail
// rules with the rulepack attribute via their junction tables, and sets rulepack_id
// on every rule. Connection associations from each rule's request body are written
// to the respective rule-connection junctions. Returns models.ErrAlreadyExists if any
// nested rule's name collides with an existing row.
func insertRulepackRulesTx(
	tx *gorm.DB,
	rp *models.Rulepack,
	attrName string,
	rules RulepackRulesInput,
) error {
	rulepackUUID := rp.ID
	rulepackUUIDStr := sql.NullString{String: rulepackUUID.String(), Valid: true}

	for _, req := range rules.DataMaskingRules {
		dmRule := &models.DataMaskingRule{
			ID:                   uuid.NewString(),
			OrgID:                rp.OrgID.String(),
			Name:                 req.Name,
			Description:          req.Description,
			SupportedEntityTypes: toModelSupportedEntityTypes(req.SupportedEntityTypes),
			CustomEntityTypes:    toModelCustomEntityTypes(req.CustomEntityTypesEntrys),
			ScoreThreshold:       req.ScoreThreshold,
			RulepackID:           rulepackUUIDStr,
			ConnectionIDs:        req.ConnectionIDs,
			UpdatedAt:            time.Now().UTC(),
		}
		if err := models.CreateDataMaskingRuleTx(tx, dmRule); err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&models.DatamaskingRuleAttribute{
				OrgID:               rp.OrgID,
				AttributeName:       attrName,
				DatamaskingRuleName: req.Name,
			}).Error; err != nil {
			return err
		}
	}

	for _, req := range rules.GuardRailRules {
		grRule := &models.GuardRailRules{
			ID:          uuid.NewString(),
			OrgID:       rp.OrgID.String(),
			Name:        req.Name,
			Description: req.Description,
			Input:       req.Input,
			Output:      req.Output,
			RulepackID:  rulepackUUIDStr,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}
		if err := models.UpsertGuardRailRuleWithConnectionsTx(tx, grRule, req.ConnectionIDs, true); err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&models.GuardrailRuleAttribute{
				OrgID:             rp.OrgID,
				AttributeName:     attrName,
				GuardrailRuleName: req.Name,
			}).Error; err != nil {
			return err
		}
	}

	for _, req := range rules.AISessionAnalyzerRules {
		aiRule := &models.AISessionAnalyzerRules{
			OrgID:           rp.OrgID,
			Name:            req.Name,
			Description:     req.Description,
			ConnectionNames: pq.StringArray(req.ConnectionNames),
			RiskEvaluation: models.AISessionAnalyzerRiskEvaluation{
				LowRiskAction:    models.RiskEvaluationAction(req.RiskEvaluation.LowRiskAction),
				MediumRiskAction: models.RiskEvaluationAction(req.RiskEvaluation.MediumRiskAction),
				HighRiskAction:   models.RiskEvaluationAction(req.RiskEvaluation.HighRiskAction),
			},
			RulepackID: &rulepackUUID,
		}
		if err := models.CreateAISessionAnalyzerRuleTx(tx, aiRule); err != nil {
			return err
		}
	}

	return nil
}

func toModelSupportedEntityTypes(in []openapi.SupportedEntityTypesEntry) []models.SupportedEntityTypesEntry {
	out := make([]models.SupportedEntityTypesEntry, len(in))
	for i, e := range in {
		out[i] = models.SupportedEntityTypesEntry{
			Name:        e.Name,
			EntityTypes: e.EntityTypes,
		}
	}
	return out
}

func toModelCustomEntityTypes(in []openapi.CustomEntityTypesEntry) []models.CustomEntityTypesEntry {
	out := make([]models.CustomEntityTypesEntry, len(in))
	for i, e := range in {
		out[i] = models.CustomEntityTypesEntry{
			Name:     e.Name,
			Regex:    e.Regex,
			DenyList: e.DenyList,
			Score:    e.Score,
		}
	}
	return out
}
