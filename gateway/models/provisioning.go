package models

import (
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// Where a provisioned user or group came from (ADR-0019). Slack is pulled by
// the directory sync, SCIM is pushed by the identity provider, and a file is
// an admin's one-off import.
const (
	ProvisioningSourceSlack = "slack"
	ProvisioningSourceSCIM  = "scim"
	ProvisioningSourceFile  = "file"
)

// managingSources are the sources that own the groups they write: while an
// org has any of their rows, login and the Users page leave groups alone. A
// file import is an admin's own edit, like the Users page, so it does not.
var managingSources = []string{ProvisioningSourceSlack, ProvisioningSourceSCIM}

// SCIMToken is the bearer token an identity provider authenticates its SCIM
// requests with. Only the hash is stored.
type SCIMToken struct {
	OrgID      string     `gorm:"column:org_id;primaryKey"`
	TokenHash  string     `gorm:"column:token_hash"`
	CreatedBy  string     `gorm:"column:created_by"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	LastUsedAt *time.Time `gorm:"column:last_used_at"`
}

func (SCIMToken) TableName() string { return "private.scim_tokens" }

// GetSCIMToken returns the org's token, or gorm.ErrRecordNotFound.
func GetSCIMToken(db *gorm.DB, orgID string) (*SCIMToken, error) {
	var t SCIMToken
	if err := db.Where("org_id = ?", orgID).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// GetSCIMTokenByHash returns the token with this hash, or gorm.ErrRecordNotFound.
func GetSCIMTokenByHash(db *gorm.DB, tokenHash string) (*SCIMToken, error) {
	var t SCIMToken
	if err := db.Where("token_hash = ?", tokenHash).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// ReplaceSCIMToken stores a new token hash for the org, discarding the old one.
func ReplaceSCIMToken(db *gorm.DB, orgID, tokenHash, createdBy string) error {
	return db.Exec(`
		INSERT INTO private.scim_tokens (org_id, token_hash, created_by, created_at, last_used_at)
		VALUES (?, ?, ?, NOW(), NULL)
		ON CONFLICT (org_id) DO UPDATE
		SET token_hash = EXCLUDED.token_hash, created_by = EXCLUDED.created_by,
			created_at = EXCLUDED.created_at, last_used_at = NULL`,
		orgID, tokenHash, createdBy).Error
}

// DeleteSCIMToken removes the org's token. Absent is not an error.
func DeleteSCIMToken(db *gorm.DB, orgID string) error {
	return db.Where("org_id = ?", orgID).Delete(&SCIMToken{}).Error
}

// TouchSCIMToken records that the token was just used. It writes at most once
// a minute: an identity provider sends many requests in a burst, and the
// admin page only needs to know the token is alive.
func TouchSCIMToken(db *gorm.DB, orgID string) error {
	return db.Exec(`
		UPDATE private.scim_tokens SET last_used_at = NOW()
		WHERE org_id = ? AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')`,
		orgID).Error
}

// DirectorySyncConfig is how the control plane pulls users and groups from
// Slack user groups. Provider is always "slack" today; the column stays so a
// second source is a new value, not a new table.
//
// AllowMemberManagedGroups accepts user groups a workspace member who is not
// an admin or owner edited last. Off by default: hoop cannot restrict who
// edits a user group, and a member-edited group would let anyone make
// themselves a reviewer.
type DirectorySyncConfig struct {
	OrgID                    string         `gorm:"column:org_id;primaryKey"`
	Provider                 string         `gorm:"column:provider"`
	GroupIDs                 pq.StringArray `gorm:"column:group_ids;type:text[]"`
	IntervalMinutes          int            `gorm:"column:interval_minutes"`
	AllowMemberManagedGroups bool           `gorm:"column:allow_member_managed_groups"`
	LastRunAt                *time.Time     `gorm:"column:last_run_at"`
	LastError                *string        `gorm:"column:last_error"`
	CreatedAt                time.Time      `gorm:"column:created_at"`
	UpdatedAt                time.Time      `gorm:"column:updated_at"`
}

func (DirectorySyncConfig) TableName() string { return "private.directory_sync_configs" }

// GetDirectorySyncConfig returns the org's sync, or gorm.ErrRecordNotFound.
func GetDirectorySyncConfig(db *gorm.DB, orgID string) (*DirectorySyncConfig, error) {
	var c DirectorySyncConfig
	if err := db.Where("org_id = ?", orgID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ListDirectorySyncConfigs returns every org's sync, for the scheduler.
func ListDirectorySyncConfigs(db *gorm.DB) ([]DirectorySyncConfig, error) {
	var out []DirectorySyncConfig
	err := db.Order("org_id").Find(&out).Error
	return out, err
}

// UpsertDirectorySyncConfig stores the sync's provider, groups, interval and
// governance choice. The last run and its error are left as they are.
func UpsertDirectorySyncConfig(db *gorm.DB, c *DirectorySyncConfig) error {
	groupIDs := c.GroupIDs
	if groupIDs == nil {
		groupIDs = pq.StringArray{}
	}
	return db.Exec(`
		INSERT INTO private.directory_sync_configs
			(org_id, provider, group_ids, interval_minutes, allow_member_managed_groups)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (org_id) DO UPDATE
		SET provider = EXCLUDED.provider, group_ids = EXCLUDED.group_ids,
			interval_minutes = EXCLUDED.interval_minutes,
			allow_member_managed_groups = EXCLUDED.allow_member_managed_groups, updated_at = NOW()`,
		c.OrgID, c.Provider, groupIDs, c.IntervalMinutes, c.AllowMemberManagedGroups).Error
}

// DeleteDirectorySyncConfig removes the org's sync. Absent is not an error.
func DeleteDirectorySyncConfig(db *gorm.DB, orgID string) error {
	return db.Where("org_id = ?", orgID).Delete(&DirectorySyncConfig{}).Error
}

// SetDirectorySyncResult records when the sync last ran and why it failed;
// errMsg nil means it succeeded.
func SetDirectorySyncResult(db *gorm.DB, orgID string, runAt time.Time, errMsg *string) error {
	return db.Exec(`UPDATE private.directory_sync_configs SET last_run_at = ?, last_error = ? WHERE org_id = ?`,
		runAt, errMsg, orgID).Error
}

// GroupsManagedByProvisioning reports whether a source owns the org's groups:
// the Slack import or SCIM has written a user or a group. It reads the rows,
// not the token or the sync config, so revoking a token does not hand groups
// back to login behind the admin's back; ClearProvisioningLinks does.
func GroupsManagedByProvisioning(db *gorm.DB, orgID string) (bool, error) {
	var managed bool
	err := db.Raw(`
		SELECT EXISTS (SELECT 1 FROM private.directory_users WHERE org_id = @org AND source IN @sources)
			OR EXISTS (SELECT 1 FROM private.directory_groups WHERE org_id = @org AND source IN @sources)`,
		map[string]any{"org": orgID, "sources": managingSources}).Scan(&managed).Error
	return managed, err
}

// ClearProvisioningLinks stops every source from managing the org's groups.
// It deletes only the links: users and their user_groups rows stay, and from
// then on login and the Users page own them again.
func ClearProvisioningLinks(tx *gorm.DB, orgID string) error {
	if err := tx.Where("org_id = ?", orgID).Delete(&DirectoryUser{}).Error; err != nil {
		return err
	}
	return tx.Where("org_id = ?", orgID).Delete(&DirectoryGroup{}).Error
}

// DirectoryUser links a hoop user to the identity provider record it came from.
type DirectoryUser struct {
	UserID     string    `gorm:"column:user_id;primaryKey"`
	OrgID      string    `gorm:"column:org_id"`
	Source     string    `gorm:"column:source"`
	ExternalID *string   `gorm:"column:external_id"`
	UserName   string    `gorm:"column:user_name"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

func (DirectoryUser) TableName() string { return "private.directory_users" }

// GetDirectoryUser returns the link of one hoop user, or gorm.ErrRecordNotFound.
func GetDirectoryUser(db *gorm.DB, orgID, userID string) (*DirectoryUser, error) {
	var u DirectoryUser
	if err := db.Where("org_id = ? AND user_id = ?", orgID, userID).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// GetDirectoryUserByExternalID returns the link carrying the provider's id, or
// gorm.ErrRecordNotFound.
func GetDirectoryUserByExternalID(db *gorm.DB, orgID, source, externalID string) (*DirectoryUser, error) {
	var u DirectoryUser
	err := db.Where("org_id = ? AND source = ? AND external_id = ?", orgID, source, externalID).First(&u).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ListDirectoryUsers returns every link of the org written by source.
func ListDirectoryUsers(db *gorm.DB, orgID, source string) ([]DirectoryUser, error) {
	var out []DirectoryUser
	err := db.Where("org_id = ? AND source = ?", orgID, source).Order("user_id").Find(&out).Error
	return out, err
}

// UpsertDirectoryUser links a hoop user to its provider record.
func UpsertDirectoryUser(db *gorm.DB, u *DirectoryUser) error {
	return db.Exec(`
		INSERT INTO private.directory_users (user_id, org_id, source, external_id, user_name)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE
		SET source = EXCLUDED.source, external_id = EXCLUDED.external_id,
			user_name = EXCLUDED.user_name, updated_at = NOW()`,
		u.UserID, u.OrgID, u.Source, u.ExternalID, u.UserName).Error
}

// DirectoryGroup is a group the identity provider describes. DisplayName is
// the name user_groups and the rules' reviewers_groups carry.
type DirectoryGroup struct {
	ID          string    `gorm:"column:id;primaryKey"`
	OrgID       string    `gorm:"column:org_id"`
	Source      string    `gorm:"column:source"`
	DisplayName string    `gorm:"column:display_name"`
	ExternalID  *string   `gorm:"column:external_id"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (DirectoryGroup) TableName() string { return "private.directory_groups" }

// GetDirectoryGroup returns one group by id, or gorm.ErrRecordNotFound.
func GetDirectoryGroup(db *gorm.DB, orgID, id string) (*DirectoryGroup, error) {
	var g DirectoryGroup
	if err := db.Where("org_id = ? AND id = ?", orgID, id).First(&g).Error; err != nil {
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

// ListDirectoryGroups returns every group of the org written by source.
func ListDirectoryGroups(db *gorm.DB, orgID, source string) ([]DirectoryGroup, error) {
	var out []DirectoryGroup
	err := db.Where("org_id = ? AND source = ?", orgID, source).Order("display_name").Find(&out).Error
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
