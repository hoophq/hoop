package models

import (
	"slices"

	"gorm.io/gorm"
)

// ReviewGroupSidecarOwners is the reserved review group name that means "the
// owners of this review's sidecar". Membership comes from
// private.sidecar_owners for that sidecar, never from private.user_groups.
const ReviewGroupSidecarOwners = "sidecar-owners"

// StripReservedReviewGroups removes ReviewGroupSidecarOwners from groups, so
// a real identity-provider group with that name never grants owner approval.
func StripReservedReviewGroups(groups []string) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if g != ReviewGroupSidecarOwners {
			out = append(out, g)
		}
	}
	return out
}

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

// IsSidecarOwner reports whether email is an owner of sidecarID and names
// exactly one active or invited user of the organization.
func IsSidecarOwner(db *gorm.DB, orgID, sidecarID, email string) (bool, error) {
	if orgID == "" || sidecarID == "" || email == "" {
		return false, nil
	}
	var ok bool
	err := db.Raw(`
		SELECT EXISTS (
			SELECT 1 FROM private.sidecar_owners o
			WHERE o.org_id = ? AND o.sidecar_id = ? AND o.email = lower(?)
		) AND (
			SELECT count(*) FROM private.users u
			WHERE u.org_id = ? AND lower(u.email) = lower(?) AND u.status IN ?
		) = 1`, orgID, sidecarID, email, orgID, email, ApproverStatuses).Scan(&ok).Error
	return ok, err
}

// ListOwnedSidecarIDs returns the ids of every sidecar in orgID that email
// owns, when email names exactly one active or invited user of orgID.
func ListOwnedSidecarIDs(db *gorm.DB, orgID, email string) ([]string, error) {
	if orgID == "" || email == "" {
		return nil, nil
	}
	var ids []string
	err := db.Raw(`
		SELECT o.sidecar_id::text
		FROM private.sidecar_owners o
		WHERE o.org_id = ? AND o.email = lower(?)
		  AND (
			SELECT count(*) FROM private.users u
			WHERE u.org_id = o.org_id AND lower(u.email) = o.email AND u.status IN ?
		  ) = 1
		ORDER BY o.sidecar_id`, orgID, email, ApproverStatuses).Scan(&ids).Error
	return ids, err
}

// ListSidecarOwnerUserIDs returns the user ids of every unambiguously matched
// owner of sidecarID, in the order the owners were set.
func ListSidecarOwnerUserIDs(db *gorm.DB, orgID, sidecarID string) ([]string, error) {
	if orgID == "" || sidecarID == "" {
		return nil, nil
	}
	var ids []string
	err := db.Raw(`
		SELECT u.id::text
		FROM private.sidecar_owners o
		JOIN private.users u ON u.org_id = o.org_id AND lower(u.email) = o.email AND u.status IN ?
		WHERE o.org_id = ? AND o.sidecar_id = ?
		  AND (
			SELECT count(*) FROM private.users u2
			WHERE u2.org_id = o.org_id AND lower(u2.email) = o.email AND u2.status IN ?
		  ) = 1
		ORDER BY o.position, u.id`, ApproverStatuses, orgID, sidecarID, ApproverStatuses).Scan(&ids).Error
	return ids, err
}

// ListSidecarOwnerEmails returns the emails of the sidecar's owners, in order.
func ListSidecarOwnerEmails(db *gorm.DB, orgID, sidecarID string) ([]string, error) {
	if orgID == "" || sidecarID == "" {
		return nil, nil
	}
	var emails []string
	err := db.Raw(`
		SELECT email FROM private.sidecar_owners
		WHERE org_id = ? AND sidecar_id = ?
		ORDER BY position`, orgID, sidecarID).Scan(&emails).Error
	return emails, err
}

// ReviewHasSidecarOwners reports whether any non-denial group row of groups
// is the reserved sidecar-owners group.
func ReviewHasSidecarOwners(groups []ReviewGroups) bool {
	return slices.ContainsFunc(groups, func(g ReviewGroups) bool {
		return !g.AddedOnDenial && g.GroupName == ReviewGroupSidecarOwners
	})
}
