package models

import (
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// DirectorySyncConfig is the org's Slack import (ADR-0019): the Slack user
// groups whose members become hoop users, and how often it runs.
//
// AllowMemberManagedGroups accepts user groups a workspace member who is not
// an admin or owner edited last. Off by default: hoop cannot restrict who
// edits a user group, and a member-edited group would let anyone make
// themselves a reviewer.
type DirectorySyncConfig struct {
	OrgID                    string         `gorm:"column:org_id;primaryKey"`
	GroupIDs                 pq.StringArray `gorm:"column:group_ids;type:text[]"`
	IntervalMinutes          int            `gorm:"column:interval_minutes"`
	AllowMemberManagedGroups bool           `gorm:"column:allow_member_managed_groups"`
	LastRunAt                *time.Time     `gorm:"column:last_run_at"`
	LastError                *string        `gorm:"column:last_error"`
	CreatedAt                time.Time      `gorm:"column:created_at"`
	UpdatedAt                time.Time      `gorm:"column:updated_at"`
}

func (DirectorySyncConfig) TableName() string { return "private.directory_sync_configs" }

// GetDirectorySyncConfig returns the org's import, or gorm.ErrRecordNotFound.
func GetDirectorySyncConfig(db *gorm.DB, orgID string) (*DirectorySyncConfig, error) {
	var c DirectorySyncConfig
	if err := db.Where("org_id = ?", orgID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ListDirectorySyncConfigs returns every org's import, for the scheduler.
func ListDirectorySyncConfigs(db *gorm.DB) ([]DirectorySyncConfig, error) {
	var out []DirectorySyncConfig
	err := db.Order("org_id").Find(&out).Error
	return out, err
}

// UpsertDirectorySyncConfig stores the import's groups, interval and
// governance choice. The last run and its error are left as they are.
func UpsertDirectorySyncConfig(db *gorm.DB, c *DirectorySyncConfig) error {
	groupIDs := c.GroupIDs
	if groupIDs == nil {
		groupIDs = pq.StringArray{}
	}
	return db.Exec(`
		INSERT INTO private.directory_sync_configs
			(org_id, group_ids, interval_minutes, allow_member_managed_groups)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (org_id) DO UPDATE
		SET group_ids = EXCLUDED.group_ids,
			interval_minutes = EXCLUDED.interval_minutes,
			allow_member_managed_groups = EXCLUDED.allow_member_managed_groups, updated_at = NOW()`,
		c.OrgID, groupIDs, c.IntervalMinutes, c.AllowMemberManagedGroups).Error
}

// DeleteDirectorySyncConfig removes the org's import. Absent is not an error.
func DeleteDirectorySyncConfig(db *gorm.DB, orgID string) error {
	return db.Where("org_id = ?", orgID).Delete(&DirectorySyncConfig{}).Error
}

// SetDirectorySyncResult records when the import last ran and why it failed;
// errMsg nil means it succeeded.
func SetDirectorySyncResult(db *gorm.DB, orgID string, runAt time.Time, errMsg *string) error {
	return db.Exec(`UPDATE private.directory_sync_configs SET last_run_at = ?, last_error = ? WHERE org_id = ?`,
		runAt, errMsg, orgID).Error
}

// GroupsManagedByProvisioning reports whether the Slack import owns the org's
// groups: it has recorded a group. It reads the rows, not the import config,
// so removing the import does not hand groups back to login behind the
// admin's back; ClearProvisioningLinks does.
func GroupsManagedByProvisioning(db *gorm.DB, orgID string) (bool, error) {
	var managed bool
	err := db.Raw(`SELECT EXISTS (SELECT 1 FROM private.directory_groups WHERE org_id = ?)`, orgID).
		Scan(&managed).Error
	return managed, err
}

// ClearProvisioningLinks stops the Slack import from managing the org's
// groups. It deletes only its records: users and their user_groups rows stay,
// and from then on login and the Users page own them again.
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

// ListGroupMemberIDs returns the ids of the users in a group, by name.
func ListGroupMemberIDs(db *gorm.DB, orgID, name string) ([]string, error) {
	var ids []string
	err := db.Raw(`
		SELECT user_id::TEXT FROM private.user_groups
		WHERE org_id = ? AND name = ? AND user_id IS NOT NULL
		ORDER BY user_id`, orgID, name).Scan(&ids).Error
	return ids, err
}
