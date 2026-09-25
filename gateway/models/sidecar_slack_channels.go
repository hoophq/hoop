package models

import (
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// SidecarSlackChannels is where the reviews of one sidecar listener are
// posted in Slack.
type SidecarSlackChannels struct {
	OrgID        string         `gorm:"column:org_id;primaryKey"`
	SidecarID    string         `gorm:"column:sidecar_id;primaryKey"`
	ListenerName string         `gorm:"column:listener_name;primaryKey"`
	Channels     pq.StringArray `gorm:"column:channels;type:text[]"`
	CreatedAt    time.Time      `gorm:"column:created_at"`
	UpdatedAt    time.Time      `gorm:"column:updated_at"`
}

func (SidecarSlackChannels) TableName() string { return "private.sidecar_slack_channels" }

// ListSidecarSlackChannels returns the rows of the sidecar's listeners.
func ListSidecarSlackChannels(db *gorm.DB, orgID, sidecarID string) ([]SidecarSlackChannels, error) {
	var out []SidecarSlackChannels
	err := db.Where("org_id = ? AND sidecar_id = ?", orgID, sidecarID).
		Order("listener_name").Find(&out).Error
	return out, err
}

// SetSidecarSlackChannels sets the channels of the listeners in rows and
// leaves every other listener as it is, so two admins who each save one
// listener do not undo each other. A row without channels clears that
// listener.
func SetSidecarSlackChannels(tx *gorm.DB, orgID, sidecarID string, rows []SidecarSlackChannels) error {
	for _, r := range rows {
		if err := tx.Where("org_id = ? AND sidecar_id = ? AND listener_name = ?", orgID, sidecarID, r.ListenerName).
			Delete(&SidecarSlackChannels{}).Error; err != nil {
			return err
		}
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
// listener goes to, or none.
func ResolveSidecarSlackChannels(db *gorm.DB, orgID, sidecarID, listenerName string) ([]string, error) {
	if listenerName == "" {
		return nil, nil
	}
	var rows []SidecarSlackChannels
	err := db.Where("org_id = ? AND sidecar_id = ? AND listener_name = ?", orgID, sidecarID, listenerName).
		Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0].Channels, nil
}

// PruneSidecarSlackChannels drops the rows of listeners that are not in keep,
// so a removed or renamed listener does not leave channels behind for a
// future listener that happens to take its name.
func PruneSidecarSlackChannels(tx *gorm.DB, orgID, sidecarID string, keep []string) error {
	q := tx.Where("org_id = ? AND sidecar_id = ?", orgID, sidecarID)
	if len(keep) > 0 {
		q = q.Where("listener_name NOT IN ?", keep)
	}
	return q.Delete(&SidecarSlackChannels{}).Error
}
