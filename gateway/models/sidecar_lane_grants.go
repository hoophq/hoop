package models

import (
	"time"

	"gorm.io/gorm"
)

// SidecarLaneGrant lets a user or a group mint a credential for one lane of
// one sidecar. One row names a subject or a group, never both.
type SidecarLaneGrant struct {
	ID           string    `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID        string    `gorm:"column:org_id"`
	SidecarID    string    `gorm:"column:sidecar_id"`
	ListenerName string    `gorm:"column:listener_name"`
	Subject      *string   `gorm:"column:subject"`
	GroupName    *string   `gorm:"column:group_name"`
	CreatedBy    string    `gorm:"column:created_by"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (SidecarLaneGrant) TableName() string { return "private.sidecar_lane_grants" }

// CreateSidecarLaneGrant stores a grant and fills its id.
func CreateSidecarLaneGrant(db *gorm.DB, g *SidecarLaneGrant) error {
	return db.Create(g).Error
}

// ListSidecarLaneGrants returns the grants of one lane, oldest first.
func ListSidecarLaneGrants(db *gorm.DB, orgID, sidecarID, listenerName string) ([]SidecarLaneGrant, error) {
	var out []SidecarLaneGrant
	err := db.Where("org_id = ? AND sidecar_id = ? AND listener_name = ?", orgID, sidecarID, listenerName).
		Order("created_at, id").Find(&out).Error
	return out, err
}

// DeleteSidecarLaneGrant removes one grant of one lane.
// gorm.ErrRecordNotFound when the lane has no grant with the id.
func DeleteSidecarLaneGrant(db *gorm.DB, orgID, sidecarID, listenerName, id string) error {
	res := db.Where("org_id = ? AND sidecar_id = ? AND listener_name = ? AND id = ?",
		orgID, sidecarID, listenerName, id).Delete(&SidecarLaneGrant{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// HasSidecarLaneGrant reports whether one of the subjects or one of the
// groups holds a grant for the lane. subjects are the names a user goes by:
// its id and its email.
func HasSidecarLaneGrant(db *gorm.DB, orgID, sidecarID, listenerName string, subjects, groups []string) (bool, error) {
	if len(subjects) == 0 {
		subjects = []string{""}
	}
	if len(groups) == 0 {
		groups = []string{""}
	}
	var count int64
	err := db.Model(&SidecarLaneGrant{}).
		Where("org_id = ? AND sidecar_id = ? AND listener_name = ? AND (subject IN ? OR group_name IN ?)",
			orgID, sidecarID, listenerName, subjects, groups).
		Count(&count).Error
	return count > 0, err
}
