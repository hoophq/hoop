package services

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
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

// CreateRulepackWithAttribute creates a rulepack and one rulepack-owned attribute in a
// single database transaction. The attribute name is derived from the rulepack's
// display_name (lowercased, slug-cased, prepended with `rulepack_`) and the attribute
// description is copied from the rulepack's description.
//
// Returns ErrRulepackInvalidDisplayName when the display_name slugs to an empty string
// (e.g., contains only unsupported characters). Returns models.ErrAlreadyExists when the
// rulepack's display_name OR the derived attribute name collides with an existing row
// in the org. Two rulepacks whose display names slug to the same value will produce
// this collision; rename one to disambiguate.
//
// is_managed is passed through unchanged from the input rulepack; callers (e.g. HTTP
// handler) are responsible for forcing it to false when needed.
func CreateRulepackWithAttribute(
	ctx context.Context,
	rp *models.Rulepack,
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
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return rp, attr, nil
}

// UpdateRulepackWithAttribute updates a rulepack and, when the derived attribute name
// changes (because rp.DisplayName changed), renames the matching rulepack-owned
// attribute and refreshes its description from rp.Description. All writes occur in a
// single transaction.
//
// The matched attribute is the one whose (org_id, rulepack_id, name) equals the OLD
// derived slug. If no attribute matches (e.g. it was manually renamed or never created),
// the rulepack still updates and no attribute is touched. Junction tables that reference
// attributes by name follow the rename automatically via ON UPDATE CASCADE.
//
// Returns ErrRulepackInvalidDisplayName when rp.DisplayName slugs to an empty string,
// models.ErrNotFound if the rulepack does not exist, and models.ErrAlreadyExists when
// the new display_name OR the new derived attribute name collides with an existing row.
//
// is_managed is not checked at this layer; HTTP handlers (or other callers) are
// responsible for blocking updates to managed rulepacks when appropriate.
func UpdateRulepackWithAttribute(
	ctx context.Context,
	rp *models.Rulepack,
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

		if oldAttrName == newAttrName {
			return nil
		}

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
		if result.RowsAffected == 0 {
			return nil
		}

		var fetched models.Attribute
		if err := tx.Where("org_id = ? AND name = ?", rp.OrgID, newAttrName).First(&fetched).Error; err != nil {
			return err
		}
		renamedAttr = &fetched
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return rp, renamedAttr, nil
}
