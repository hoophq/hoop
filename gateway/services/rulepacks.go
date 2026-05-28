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
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const RulepackAttributeNamePrefix = "rulepack_"

const (
	rulepackRuleNamePrefixWord = "rp_"
	rulepackRuleNameSeparator  = "__"
	rulepackRuleIDPrefixLen    = 8
)

// RulepackRuleNamePrefix returns the deterministic prefix that namespaces every
// data masking and guardrail rule created via a rulepack. The prefix uses the
// first 8 hex characters of the rulepack UUID so that two rulepacks in the same
// organization can carry rules with identical user-typed names without
// colliding on the (org_id, name) unique constraint of the rule tables.
//
// Format: "rp_<uuid8>__"  e.g. "rp_15b5a2fd__"
//
// The full stored name is `<prefix><user-typed-name>`, which fits the rule
// column's VARCHAR(128) as long as the user name is <= 116 chars.
func RulepackRuleNamePrefix(rulepackID uuid.UUID) string {
	short := rulepackID.String()
	if len(short) > rulepackRuleIDPrefixLen {
		short = short[:rulepackRuleIDPrefixLen]
	}
	return rulepackRuleNamePrefixWord + short + rulepackRuleNameSeparator
}

// BuildRulepackRuleName prepends the rulepack prefix to a user-typed rule name.
func BuildRulepackRuleName(rulepackID uuid.UUID, userName string) string {
	return RulepackRuleNamePrefix(rulepackID) + userName
}

// StripRulepackRuleName removes the prefix from a stored rule name belonging to
// the given rulepack and returns the original user-typed name. If the stored
// name does not carry the expected prefix (e.g. the rule was created outside
// the rulepack flow), it is returned unchanged.
func StripRulepackRuleName(rulepackID uuid.UUID, stored string) string {
	prefix := RulepackRuleNamePrefix(rulepackID)
	if strings.HasPrefix(stored, prefix) {
		return stored[len(prefix):]
	}
	return stored
}

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
	ErrRulepackHasNoAttribute     = errors.New("rulepack has no associated attribute; cannot apply to connections")
)

// ConnectionsNotFoundError is returned when one or more connection names supplied
// to an apply operation do not exist in the organization. Callers can inspect the
// Names slice to surface the missing names back to the user.
type ConnectionsNotFoundError struct {
	Names []string
}

func (e *ConnectionsNotFoundError) Error() string {
	return "connections not found: " + strings.Join(e.Names, ", ")
}

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
	DataMaskingRules []openapi.DataMaskingRuleRequest
	GuardRailRules   []openapi.GuardRailRuleRequest
}

// CreateRulepackWithRules creates a rulepack, one rulepack-owned attribute, and any
// nested rules (data masking, guardrails) in a single transaction.
//
// The attribute name is derived from the rulepack's display_name (lowercased,
// slug-cased, prepended with `rulepack_`); the attribute description is copied from
// the rulepack's description. Every nested rule is also tagged with the rulepack
// attribute via its respective junction so that the rule matches connections sharing
// that attribute at session time.
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
// guardrails) with the supplied lists. All writes occur in a single transaction.
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
		storedName := BuildRulepackRuleName(rulepackUUID, req.Name)
		dmRule := &models.DataMaskingRule{
			ID:                   uuid.NewString(),
			OrgID:                rp.OrgID.String(),
			Name:                 storedName,
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
				DatamaskingRuleName: storedName,
			}).Error; err != nil {
			return err
		}
	}

	for _, req := range rules.GuardRailRules {
		storedName := BuildRulepackRuleName(rulepackUUID, req.Name)
		grRule := &models.GuardRailRules{
			ID:          uuid.NewString(),
			OrgID:       rp.OrgID.String(),
			Name:        storedName,
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
				GuardrailRuleName: storedName,
			}).Error; err != nil {
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

// ApplyRulepackToConnections sets the rulepack's auto-derived attribute on exactly
// the connections in connectionNames. Connections that previously had the rulepack
// attribute but are not in the list lose it; connections in the list that did not
// have it gain it. Other (non-rulepack) attributes on these connections are
// untouched. Pass an empty list to remove the rulepack from every connection.
//
// The operation is idempotent — re-running with the same list produces the same
// end state.
//
// Returns models.ErrNotFound when the rulepack does not exist. Returns a
// *ConnectionsNotFoundError when one or more connection names do not exist in the
// org (no junction rows are written; nothing partially applies). Returns
// ErrRulepackHasNoAttribute as a defensive signal if the rulepack somehow has no
// associated attribute (should not occur for rulepacks created via the rulepack
// API).
func ApplyRulepackToConnections(
	ctx context.Context,
	orgID, rulepackID uuid.UUID,
	connectionNames []string,
) error {
	return models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := models.GetRulepack(tx, orgID, rulepackID); err != nil {
			return err
		}

		var attrName string
		err := tx.Model(&models.Attribute{}).
			Select("name").
			Where("org_id = ? AND rulepack_id = ?", orgID, rulepackID).
			Limit(1).
			Scan(&attrName).Error
		if err != nil {
			return err
		}
		if attrName == "" {
			return ErrRulepackHasNoAttribute
		}

		if len(connectionNames) > 0 {
			var existing []string
			if err := tx.Table("private.connections").
				Where("org_id = ? AND name IN ?", orgID, connectionNames).
				Pluck("name", &existing).Error; err != nil {
				return err
			}
			if len(existing) < len(connectionNames) {
				present := make(map[string]struct{}, len(existing))
				for _, n := range existing {
					present[n] = struct{}{}
				}
				missing := make([]string, 0, len(connectionNames)-len(existing))
				for _, n := range connectionNames {
					if _, ok := present[n]; !ok {
						missing = append(missing, n)
					}
				}
				return &ConnectionsNotFoundError{Names: missing}
			}
		}

		if err := tx.Where("org_id = ? AND attribute_name = ?", orgID, attrName).
			Delete(&models.ConnectionAttribute{}).Error; err != nil {
			return err
		}

		if len(connectionNames) == 0 {
			return nil
		}

		rows := make([]models.ConnectionAttribute, 0, len(connectionNames))
		for _, name := range connectionNames {
			rows = append(rows, models.ConnectionAttribute{
				OrgID:          orgID,
				AttributeName:  attrName,
				ConnectionName: name,
			})
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	})
}
