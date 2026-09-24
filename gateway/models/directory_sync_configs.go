package models

import (
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// DirectorySyncConfig is the org's Slack import (ADR-0020): the Slack user
// groups whose members become hoop users, and how often it runs.
type DirectorySyncConfig struct {
	OrgID           string         `gorm:"column:org_id;primaryKey"`
	GroupIDs        pq.StringArray `gorm:"column:group_ids;type:text[]"`
	IntervalMinutes int            `gorm:"column:interval_minutes"`
	LastRunAt       *time.Time     `gorm:"column:last_run_at"`
	LastError       *string        `gorm:"column:last_error"`
	CreatedAt       time.Time      `gorm:"column:created_at"`
	UpdatedAt       time.Time      `gorm:"column:updated_at"`
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

// UpsertDirectorySyncConfig stores the import's groups and interval. The last
// run and its error are left as they are.
func UpsertDirectorySyncConfig(db *gorm.DB, c *DirectorySyncConfig) error {
	groupIDs := c.GroupIDs
	if groupIDs == nil {
		groupIDs = pq.StringArray{}
	}
	return db.Exec(`
		INSERT INTO private.directory_sync_configs
			(org_id, group_ids, interval_minutes)
		VALUES (?, ?, ?)
		ON CONFLICT (org_id) DO UPDATE
		SET group_ids = EXCLUDED.group_ids,
			interval_minutes = EXCLUDED.interval_minutes, updated_at = NOW()`,
		c.OrgID, groupIDs, c.IntervalMinutes).Error
}

// DeleteDirectorySyncConfig removes the org's import. Absent is not an error.
func DeleteDirectorySyncConfig(db *gorm.DB, orgID string) error {
	return db.Where("org_id = ?", orgID).Delete(&DirectorySyncConfig{}).Error
}

// SetDirectorySyncResult records when the import last ran and why it failed;
// errMsg nil means it succeeded. It writes only while the import's updated_at
// is still generation, the settings the run read: a save or a removal during
// the run keeps the new settings' own result.
func SetDirectorySyncResult(db *gorm.DB, orgID string, generation, runAt time.Time, errMsg *string) error {
	return db.Exec(`UPDATE private.directory_sync_configs SET last_run_at = ?, last_error = ?
		WHERE org_id = ? AND updated_at = ?`,
		runAt, errMsg, orgID, generation).Error
}
