package models

import (
	"database/sql"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/liveevents"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// EscalatedReview is one pending review whose escalation delay elapsed, after
// its escalation groups were added to private.review_groups.
type EscalatedReview struct {
	ReviewID       string
	OrgID          string
	SessionID      string
	SidecarName    string
	ListenerName   string
	ConnectionName string
	OwnerID        string
	OwnerEmail     string
	Statement      string
	Groups         []string
	ExpiresAt      *time.Time
}

// ClaimDueEscalations claims up to limit pending reviews whose escalate_at has
// passed and have not yet expired or escalated. For each review, it stamps
// escalated_at and inserts any missing escalation group into
// private.review_groups inside one transaction with FOR UPDATE SKIP LOCKED, so
// concurrent replicas take disjoint slices and never escalate a review twice.
func ClaimDueEscalations(db *gorm.DB, now time.Time, limit int) ([]EscalatedReview, error) {
	var claimed []EscalatedReview
	err := db.Transaction(func(tx *gorm.DB) error {
		var rows []struct {
			ID             string         `gorm:"column:id"`
			OrgID          string         `gorm:"column:org_id"`
			SessionID      string         `gorm:"column:session_id"`
			ConnectionName string         `gorm:"column:connection_name"`
			ListenerName   sql.NullString `gorm:"column:listener_name"`
			OwnerID        string         `gorm:"column:owner_id"`
			OwnerEmail     string         `gorm:"column:owner_email"`
			OwnerName      *string        `gorm:"column:owner_name"`
			BlobInputID    sql.NullString `gorm:"column:blob_input_id"`
			ExpiresAt      *time.Time     `gorm:"column:expires_at"`
			EscalateGroups pq.StringArray `gorm:"column:escalate_groups;type:text[]"`
		}
		err := tx.Raw(`
			SELECT id, org_id, session_id, connection_name, listener_name,
			       owner_id, owner_email, owner_name, blob_input_id, expires_at, escalate_groups
			FROM private.reviews
			WHERE status = ?
			  AND escalate_at IS NOT NULL
			  AND escalate_at <= ?
			  AND escalated_at IS NULL
			  AND (expires_at IS NULL OR expires_at > ?)
			ORDER BY escalate_at
			LIMIT ?
			FOR UPDATE SKIP LOCKED`,
			ReviewStatusPending, now, now, limit).Scan(&rows).Error
		if err != nil {
			return err
		}
		for _, r := range rows {
			groups := StripReservedReviewGroups([]string(r.EscalateGroups))
			if err := tx.Exec(`UPDATE private.reviews SET escalated_at = ? WHERE id = ?`, now, r.ID).Error; err != nil {
				return err
			}
			if len(groups) == 0 {
				continue
			}
			var existing []string
			if err := tx.Raw(`SELECT group_name FROM private.review_groups WHERE review_id = ? AND NOT added_on_denial`,
				r.ID).Scan(&existing).Error; err != nil {
				return err
			}
			for _, g := range groups {
				if slices.Contains(existing, g) {
					continue
				}
				if err := tx.Table("private.review_groups").Create(map[string]any{
					"id":         uuid.NewString(),
					"org_id":     r.OrgID,
					"review_id":  r.ID,
					"group_name": g,
					"status":     ReviewStatusPending,
				}).Error; err != nil {
					return err
				}
				existing = append(existing, g)
			}
			var stmt string
			if r.BlobInputID.Valid && r.BlobInputID.String != "" {
				var raw json.RawMessage
				if err := tx.Raw(`SELECT blob_stream FROM private.blobs WHERE org_id = ? AND id = ?`,
					r.OrgID, r.BlobInputID.String).Row().Scan(&raw); err == nil && len(raw) > 0 {
					var parts []string
					if json.Unmarshal(raw, &parts) == nil && len(parts) > 0 {
						stmt = parts[0]
					}
				}
			}
			sidecarName := ""
			if r.OwnerName != nil {
				sidecarName = *r.OwnerName
			}
			claimed = append(claimed, EscalatedReview{
				ReviewID:       r.ID,
				OrgID:          r.OrgID,
				SessionID:      r.SessionID,
				SidecarName:    sidecarName,
				ListenerName:   r.ListenerName.String,
				ConnectionName: r.ConnectionName,
				OwnerID:        r.OwnerID,
				OwnerEmail:     r.OwnerEmail,
				Statement:      stmt,
				Groups:         groups,
				ExpiresAt:      r.ExpiresAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, esc := range claimed {
		if featureflag.IsEnabled(esc.OrgID, featureflag.FlagReviewNotifications) {
			liveevents.Publish(esc.OrgID, liveevents.Event{
				Type:     liveevents.ReviewCreated,
				ID:       esc.ReviewID,
				Audience: &liveevents.Audience{Groups: esc.Groups, OwnerID: esc.OwnerID},
			})
		}
	}
	return claimed, nil
}

// ListGroupMemberSlackIDs returns the distinct non-empty slack_ids of every
// active or invited user in orgID who belongs to at least one group in groups.
func ListGroupMemberSlackIDs(db *gorm.DB, orgID string, groups []string) ([]string, error) {
	groups = StripReservedReviewGroups(groups)
	if orgID == "" || len(groups) == 0 {
		return nil, nil
	}
	var ids []string
	err := db.Raw(`
		SELECT DISTINCT u.slack_id
		FROM private.users u
		JOIN private.user_groups ug ON ug.user_id = u.id AND ug.org_id = u.org_id
		WHERE u.org_id = ? AND u.status IN ? AND ug.name IN ? AND COALESCE(u.slack_id, '') <> ''
		ORDER BY u.slack_id`, orgID, ApproverStatuses, groups).Scan(&ids).Error
	if err != nil {
		log.With("org", orgID).Warnf("failed listing slack ids of escalation groups, reason=%v", err)
		return nil, err
	}
	return ids, nil
}
