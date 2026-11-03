package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SidecarLoginPending  = "pending"
	SidecarLoginApproved = "approved"
	SidecarLoginDenied   = "denied"
)

// SidecarLogin is a sidecar asking, by device code, to join the organization
// of the admin who approves it. KeyHash is the hash of the token the sidecar
// made for itself: approving creates a sidecar with that hash, so the plane
// never holds the token.
type SidecarLogin struct {
	ID            string    `gorm:"column:id"`
	UserCode      string    `gorm:"column:user_code"`
	KeyHash       string    `gorm:"column:key_hash"`
	RequestedName string    `gorm:"column:requested_name"`
	Status        string    `gorm:"column:status"`
	OrgID         *string   `gorm:"column:org_id"`
	SidecarID     *string   `gorm:"column:sidecar_id"`
	DecidedBy     *string   `gorm:"column:decided_by"`
	ExpiresAt     time.Time `gorm:"column:expires_at"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

// Expired is whether the code can no longer be decided.
func (l SidecarLogin) Expired(now time.Time) bool { return !now.Before(l.ExpiresAt) }

// CreateSidecarLogin stores a pending login. ErrAlreadyExists means the user
// code or the token hash is taken; the caller makes a new code and retries.
func CreateSidecarLogin(db *gorm.DB, l *SidecarLogin) error {
	if l.KeyHash == "" || l.UserCode == "" {
		return errors.New("a sidecar login needs a user code and a key hash")
	}
	l.Status = SidecarLoginPending
	l.CreatedAt = time.Now().UTC()
	err := db.Table("private.sidecar_logins").Create(map[string]any{
		"user_code":      l.UserCode,
		"key_hash":       l.KeyHash,
		"requested_name": l.RequestedName,
		"status":         l.Status,
		"expires_at":     l.ExpiresAt,
		"created_at":     l.CreatedAt,
	}).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrAlreadyExists
	}
	return err
}

// GetSidecarLoginByKeyHash returns the login the sidecar holding the token polls.
func GetSidecarLoginByKeyHash(db *gorm.DB, keyHash string) (*SidecarLogin, error) {
	var l SidecarLogin
	err := db.Table("private.sidecar_logins").Where("key_hash = ?", keyHash).Take(&l).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// GetSidecarLoginByUserCode returns the login an admin is looking at.
func GetSidecarLoginByUserCode(db *gorm.DB, userCode string) (*SidecarLogin, error) {
	var l SidecarLogin
	err := db.Table("private.sidecar_logins").Where("user_code = ?", userCode).Take(&l).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// GetSidecarLoginByUserCodeForUpdate locks the row, so two admins deciding
// one code cannot both create a sidecar from it.
func GetSidecarLoginByUserCodeForUpdate(tx *gorm.DB, userCode string) (*SidecarLogin, error) {
	var l SidecarLogin
	err := tx.Table("private.sidecar_logins").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_code = ?", userCode).Take(&l).Error
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// DecideSidecarLogin records the decision on a pending login. orgID and
// sidecarID are empty for a denial.
func DecideSidecarLogin(tx *gorm.DB, id, status, orgID, sidecarID, decidedBy string) error {
	values := map[string]any{"status": status, "decided_by": decidedBy}
	if orgID != "" {
		values["org_id"] = orgID
	}
	if sidecarID != "" {
		values["sidecar_id"] = sidecarID
	}
	res := tx.Table("private.sidecar_logins").
		Where("id = ? AND status = ?", id, SidecarLoginPending).
		Updates(values)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteExpiredSidecarLogins removes undecided logins past their expiry and
// decided ones older than keep. A decided row is kept a while so a sidecar
// that polls late still reads its answer.
func DeleteExpiredSidecarLogins(db *gorm.DB, now time.Time, keep time.Duration) error {
	return db.Exec(`
	DELETE FROM private.sidecar_logins
	WHERE (status = ? AND expires_at < ?) OR created_at < ?`,
		SidecarLoginPending, now, now.Add(-keep)).Error
}

// CountPendingSidecarLogins is how many undecided, unexpired logins exist
// across every organization. The start route is unauthenticated, so this
// bounds the table rather than any one caller.
func CountPendingSidecarLogins(db *gorm.DB, now time.Time) (int64, error) {
	var n int64
	err := db.Table("private.sidecar_logins").
		Where("status = ? AND expires_at >= ?", SidecarLoginPending, now).
		Count(&n).Error
	return n, err
}

// LockOrgSidecars serializes sidecar creation inside one organization for the
// rest of the transaction, so a seat check and the insert after it cannot
// interleave with another creation.
func LockOrgSidecars(tx *gorm.DB, orgID string) error {
	return tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('private.sidecars:' || ?))`, orgID).Error
}

// CountSidecars is how many sidecars the organization holds.
func CountSidecars(db *gorm.DB, orgID string) (int64, error) {
	var n int64
	err := db.Table("private.sidecars").Where("org_id = ?", orgID).Count(&n).Error
	return n, err
}

// OldestSidecarIDs returns the ids of the n oldest sidecars of the
// organization: the ones a free tier of n seats keeps serving.
func OldestSidecarIDs(db *gorm.DB, orgID string, n int) ([]string, error) {
	var ids []string
	err := db.Table("private.sidecars").
		Where("org_id = ?", orgID).
		Order("created_at ASC, id ASC").
		Limit(n).
		Pluck("id", &ids).Error
	return ids, err
}
