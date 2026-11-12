package models

import (
	"encoding/json"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// LibraryRule is one rule of the organization's guardrail or data masking
// library that carries a sidecar block, bound to a listener or not. An
// analyzer risk level names these rules for the AI to apply (ENG-662).
type LibraryRule struct {
	Name string
	Spec json.RawMessage `gorm:"column:sidecar_spec"`
}

// ListGuardrailSidecarLibrary returns every guardrail rule of the
// organization with a sidecar block, ordered by name so a composed document
// is byte-stable. Rulepack rules are left out, as the library page does.
func ListGuardrailSidecarLibrary(db *gorm.DB, orgID uuid.UUID) ([]LibraryRule, error) {
	return listSidecarLibrary(db, "private.guardrail_rules", orgID)
}

// ListDataMaskingSidecarLibrary is ListGuardrailSidecarLibrary for data
// masking rules.
func ListDataMaskingSidecarLibrary(db *gorm.DB, orgID uuid.UUID) ([]LibraryRule, error) {
	return listSidecarLibrary(db, "private.datamasking_rules", orgID)
}

// listSidecarLibrary reads one library. The table name comes from the
// callers, never from input.
func listSidecarLibrary(db *gorm.DB, table string, orgID uuid.UUID) ([]LibraryRule, error) {
	var out []LibraryRule
	err := db.Raw(`
	SELECT name, sidecar_spec
	FROM `+table+`
	WHERE org_id = ? AND rulepack_id IS NULL
	AND sidecar_spec IS NOT NULL AND jsonb_typeof(sidecar_spec) <> 'null'
	ORDER BY name`, orgID).Scan(&out).Error
	return out, err
}
