package models

import (
	"fmt"
	"slices"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AccessRequestRuleSidecar is one sidecar a sidecar rule authorizes to file
// reviews under it.
type AccessRequestRuleSidecar struct {
	OrgID          uuid.UUID `gorm:"column:org_id;primaryKey"`
	AccessRuleName string    `gorm:"column:access_rule_name;primaryKey"`
	SidecarID      string    `gorm:"column:sidecar_id;primaryKey"`
}

func (AccessRequestRuleSidecar) TableName() string {
	return "private.access_request_rules_sidecars"
}

// SetAccessRequestRuleSidecars replaces the sidecars a rule authorizes. Names
// resolve inside the organization. An unknown name fails the whole write with
// gorm.ErrRecordNotFound, so a typo never leaves a rule authorizing less than
// the admin asked for.
func SetAccessRequestRuleSidecars(db *gorm.DB, orgID uuid.UUID, ruleName string, sidecarNames []string) error {
	names := slices.Compact(slices.Sorted(slices.Values(sidecarNames)))
	return db.Transaction(func(tx *gorm.DB) error {
		var sidecars []struct {
			ID   string
			Name string
		}
		if len(names) > 0 {
			err := tx.Raw(`SELECT id, name FROM private.sidecars WHERE org_id = ? AND name IN ?`, orgID, names).
				Scan(&sidecars).
				Error
			if err != nil {
				return err
			}
		}
		if len(sidecars) != len(names) {
			found := make(map[string]bool, len(sidecars))
			for _, s := range sidecars {
				found[s.Name] = true
			}
			for _, name := range names {
				if !found[name] {
					return fmt.Errorf("sidecar %q: %w", name, gorm.ErrRecordNotFound)
				}
			}
		}

		if err := tx.Where("org_id = ? AND access_rule_name = ?", orgID, ruleName).
			Delete(&AccessRequestRuleSidecar{}).Error; err != nil {
			return err
		}
		if len(sidecars) == 0 {
			return nil
		}
		assocs := make([]AccessRequestRuleSidecar, len(sidecars))
		for i, s := range sidecars {
			assocs[i] = AccessRequestRuleSidecar{OrgID: orgID, AccessRuleName: ruleName, SidecarID: s.ID}
		}
		return tx.Create(&assocs).Error
	})
}

// ListAccessRequestRuleSidecarNames returns the sidecar names of each given
// rule, sorted. A rule that authorizes no sidecar has no key.
func ListAccessRequestRuleSidecarNames(db *gorm.DB, orgID uuid.UUID, ruleNames []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(ruleNames) == 0 {
		return out, nil
	}
	var rows []struct {
		AccessRuleName string
		Name           string
	}
	err := db.Raw(`
	SELECT ars.access_rule_name, s.name
	FROM private.access_request_rules_sidecars ars
	JOIN private.sidecars s ON s.org_id = ars.org_id AND s.id = ars.sidecar_id
	WHERE ars.org_id = ? AND ars.access_rule_name IN ?
	ORDER BY s.name`, orgID, ruleNames).
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.AccessRuleName] = append(out[r.AccessRuleName], r.Name)
	}
	return out, nil
}
