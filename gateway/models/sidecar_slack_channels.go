package models

import (
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// SidecarSlackChannels is where the reviews of one sidecar, or of one of its
// listeners, are posted in Slack. ListenerName "" is the whole sidecar.
type SidecarSlackChannels struct {
	OrgID        string         `gorm:"column:org_id;primaryKey"`
	SidecarID    string         `gorm:"column:sidecar_id;primaryKey"`
	ListenerName string         `gorm:"column:listener_name;primaryKey"`
	Channels     pq.StringArray `gorm:"column:channels;type:text[]"`
	CreatedAt    time.Time      `gorm:"column:created_at"`
	UpdatedAt    time.Time      `gorm:"column:updated_at"`
}

func (SidecarSlackChannels) TableName() string { return "private.sidecar_slack_channels" }

// ListSidecarSlackChannels returns the sidecar's rows, the sidecar's own first.
func ListSidecarSlackChannels(db *gorm.DB, orgID, sidecarID string) ([]SidecarSlackChannels, error) {
	var out []SidecarSlackChannels
	err := db.Where("org_id = ? AND sidecar_id = ?", orgID, sidecarID).
		Order("listener_name").Find(&out).Error
	return out, err
}

// ReplaceSidecarSlackChannels makes rows the sidecar's complete set. A row
// without channels is skipped: no row is what "inherit" means.
func ReplaceSidecarSlackChannels(tx *gorm.DB, orgID, sidecarID string, rows []SidecarSlackChannels) error {
	if err := tx.Where("org_id = ? AND sidecar_id = ?", orgID, sidecarID).
		Delete(&SidecarSlackChannels{}).Error; err != nil {
		return err
	}
	for _, r := range rows {
		if len(r.Channels) == 0 {
			continue
		}
		if err := tx.Exec(`
			INSERT INTO private.sidecar_slack_channels (org_id, sidecar_id, listener_name, channels)
			VALUES (?, ?, ?, ?)`, orgID, sidecarID, r.ListenerName, r.Channels).Error; err != nil {
			return err
		}
	}
	return nil
}

// ResolveSidecarSlackChannels returns the channels a review from this
// listener goes to: the listener's own, else the sidecar's, else none.
func ResolveSidecarSlackChannels(db *gorm.DB, orgID, sidecarID, listenerName string) ([]string, error) {
	rows, err := ListSidecarSlackChannels(db, orgID, sidecarID)
	if err != nil {
		return nil, err
	}
	var sidecarWide []string
	for _, r := range rows {
		if listenerName != "" && r.ListenerName == listenerName {
			return r.Channels, nil
		}
		if r.ListenerName == "" {
			sidecarWide = r.Channels
		}
	}
	return sidecarWide, nil
}

// PruneSidecarSlackChannels drops the rows of listeners that are not in keep,
// so a removed or renamed listener does not leave channels behind for a
// future listener that happens to take its name.
func PruneSidecarSlackChannels(tx *gorm.DB, orgID, sidecarID string, keep []string) error {
	q := tx.Where("org_id = ? AND sidecar_id = ? AND listener_name <> ''", orgID, sidecarID)
	if len(keep) > 0 {
		q = q.Where("listener_name NOT IN ?", keep)
	}
	return q.Delete(&SidecarSlackChannels{}).Error
}
