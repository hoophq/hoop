package models

import (
	"time"

	"gorm.io/gorm"
)

// GroupsManagedByProvisioning reports whether the Slack import owns the org's
// groups: it has recorded a group. Removing the import clears the records.
func GroupsManagedByProvisioning(db *gorm.DB, orgID string) (bool, error) {
	var managed bool
	err := db.Raw(`SELECT EXISTS (SELECT 1 FROM private.directory_groups WHERE org_id = ?)`, orgID).
		Scan(&managed).Error
	return managed, err
}

// ClearProvisioningLinks forgets which groups the Slack import owns. Users and
// their user_groups rows stay, and from then on login owns them again.
func ClearProvisioningLinks(tx *gorm.DB, orgID string) error {
	return tx.Where("org_id = ?", orgID).Delete(&DirectoryGroup{}).Error
}

// DirectoryGroup is a hoop group the Slack import owns. ExternalID is the
// Slack user group id, and DisplayName the name user_groups and the rules'
// reviewers_groups carry.
type DirectoryGroup struct {
	OrgID       string    `gorm:"column:org_id;primaryKey"`
	ExternalID  string    `gorm:"column:external_id;primaryKey"`
	DisplayName string    `gorm:"column:display_name"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (DirectoryGroup) TableName() string { return "private.directory_groups" }

// GetDirectoryGroup returns the group of one Slack user group, or
// gorm.ErrRecordNotFound.
func GetDirectoryGroup(db *gorm.DB, orgID, externalID string) (*DirectoryGroup, error) {
	var g DirectoryGroup
	if err := db.Where("org_id = ? AND external_id = ?", orgID, externalID).First(&g).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

// GetDirectoryGroupByName returns one group by display name, or
// gorm.ErrRecordNotFound.
func GetDirectoryGroupByName(db *gorm.DB, orgID, displayName string) (*DirectoryGroup, error) {
	var g DirectoryGroup
	if err := db.Where("org_id = ? AND display_name = ?", orgID, displayName).First(&g).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

// ListDirectoryGroups returns every group the Slack import owns in the org.
func ListDirectoryGroups(db *gorm.DB, orgID string) ([]DirectoryGroup, error) {
	var out []DirectoryGroup
	err := db.Where("org_id = ?", orgID).Order("display_name").Find(&out).Error
	return out, err
}
