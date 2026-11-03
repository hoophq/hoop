package models

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// SidecarInstanceRetention is how long a silent instance row is kept. Pods
// come and go; a rolling restart would otherwise leave a row forever.
const SidecarInstanceRetention = 24 * time.Hour

// SidecarInstance is one process behind a sidecar, named by the hostname it
// sends in daemon.SidecarInstanceHeader. Pods sharing one credential reach
// one Sidecar row and one row each here.
type SidecarInstance struct {
	OrgID     string `gorm:"column:org_id"`
	SidecarID string `gorm:"column:sidecar_id"`
	Name      string `gorm:"column:name"`

	// What this instance last reported. Nullable like the Sidecar fields:
	// a build that reports nothing reads as unknown.
	ReportedVersion *string   `gorm:"column:reported_version"`
	AppliedRevision *string   `gorm:"column:applied_revision"`
	LastOutcome     *string   `gorm:"column:last_outcome"`
	LastError       *string   `gorm:"column:last_error"`
	FirstSeenAt     time.Time `gorm:"column:first_seen_at"`
	LastSeenAt      time.Time `gorm:"column:last_seen_at"`
}

// RecordSidecarInstanceHandshake stores what one instance said on its
// handshake, inserting its row on the first. The row says what the pod said
// last; the folding rules of RecordSidecarHandshake belong to the sidecar
// row, which merges every pod.
func RecordSidecarInstanceHandshake(db *gorm.DB, orgID, sidecarID, name, version, appliedRevision, lastOutcome, lastError string) error {
	return db.Exec(`
	INSERT INTO private.sidecar_instances
		(org_id, sidecar_id, name, reported_version, applied_revision, last_outcome, last_error)
	VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))
	ON CONFLICT (sidecar_id, name) DO UPDATE SET
		last_seen_at = NOW(),
		reported_version = EXCLUDED.reported_version,
		applied_revision = EXCLUDED.applied_revision,
		last_outcome = EXCLUDED.last_outcome,
		last_error = EXCLUDED.last_error`,
		orgID, sidecarID, name, version, appliedRevision, lastOutcome, lastError).Error
}

// ListSidecarInstances returns every instance of one sidecar, by name.
func ListSidecarInstances(db *gorm.DB, sidecarID string) ([]SidecarInstance, error) {
	var items []SidecarInstance
	err := db.Raw(`
	SELECT org_id, sidecar_id, name, reported_version, applied_revision, last_outcome, last_error,
		first_seen_at, last_seen_at
	FROM private.sidecar_instances
	WHERE sidecar_id = ?
	ORDER BY name`, sidecarID).Scan(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// DeleteStaleSidecarInstances removes, in every org, each instance silent for
// longer than olderThan. It returns how many it removed.
func DeleteStaleSidecarInstances(ctx context.Context, db *gorm.DB, olderThan time.Duration) (int64, error) {
	res := db.WithContext(ctx).Exec(`
	DELETE FROM private.sidecar_instances
	WHERE last_seen_at < NOW() - make_interval(secs => ?)`, olderThan.Seconds())
	return res.RowsAffected, res.Error
}
