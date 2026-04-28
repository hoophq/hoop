package models

import (
	"time"
)

type OrgFeatureFlag struct {
	OrgID     string    `gorm:"column:org_id;primaryKey"`
	Name      string    `gorm:"column:name;primaryKey"`
	Enabled   bool      `gorm:"column:enabled"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
	UpdatedBy *string   `gorm:"column:updated_by"`
}

func (OrgFeatureFlag) TableName() string { return "private.org_feature_flags" }

func ListOrgFeatureFlags(orgID string) ([]OrgFeatureFlag, error) {
	var flags []OrgFeatureFlag
	err := DB.Where("org_id = ?", orgID).Find(&flags).Error
	return flags, err
}

func UpsertOrgFeatureFlag(orgID, name string, enabled bool, updatedBy string) error {
	flag := OrgFeatureFlag{
		OrgID:     orgID,
		Name:      name,
		Enabled:   enabled,
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: &updatedBy,
	}
	return DB.Exec(`
		INSERT INTO private.org_feature_flags (org_id, name, enabled, updated_at, updated_by)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (org_id, name)
		DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`,
		flag.OrgID, flag.Name, flag.Enabled, flag.UpdatedAt, flag.UpdatedBy,
	).Error
}
