package models

import "gorm.io/gorm"

// SidecarOwner is one person responsible for a sidecar, named by email.
type SidecarOwner struct {
	Email string `gorm:"column:email"`
	// Matched is true when exactly one user of the org that may approve
	// (ApproverStatuses) has the email. Two users with one email identify
	// nobody, so they do not match.
	Matched bool `gorm:"column:matched"`
}

// ListSidecarOwners returns the owners of the sidecar in the order they were
// set.
func ListSidecarOwners(db *gorm.DB, orgID, sidecarID string) ([]SidecarOwner, error) {
	var out []SidecarOwner
	err := db.Raw(`
		SELECT o.email, (
			SELECT count(*) FROM private.users u
			WHERE u.org_id = o.org_id AND lower(u.email) = o.email AND u.status IN ?
		) = 1 AS matched
		FROM private.sidecar_owners o
		WHERE o.org_id = ? AND o.sidecar_id = ?
		ORDER BY o.position`, ApproverStatuses, orgID, sidecarID).Scan(&out).Error
	return out, err
}

// SetSidecarOwners replaces the owners of the sidecar with emails, in that
// order. The emails are lowercase and distinct: the caller normalizes them.
func SetSidecarOwners(tx *gorm.DB, orgID, sidecarID string, emails []string) error {
	if err := tx.Exec(`DELETE FROM private.sidecar_owners WHERE org_id = ? AND sidecar_id = ?`,
		orgID, sidecarID).Error; err != nil {
		return err
	}
	for i, email := range emails {
		if err := tx.Exec(`
			INSERT INTO private.sidecar_owners (org_id, sidecar_id, email, position)
			VALUES (?, ?, ?, ?)`, orgID, sidecarID, email, i).Error; err != nil {
			return err
		}
	}
	return nil
}
