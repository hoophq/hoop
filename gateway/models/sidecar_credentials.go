package models

import (
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// SidecarCredentialType is the connection_type of a credential minted for a
// sidecar lane. Such a credential names no connection row: its
// connection_name is SidecarCredentialConnectionName.
const SidecarCredentialType = "sidecar"

const sidecarCredentialPrefix = "sidecar:"

// SidecarCredentialConnectionName is the connection_name of a credential for
// one lane of one sidecar.
func SidecarCredentialConnectionName(sidecarName, lane string) string {
	return sidecarCredentialPrefix + sidecarName + "/" + lane
}

// ParseSidecarCredentialConnectionName splits a sidecar credential's
// connection_name into the sidecar name and the lane. ok is false for any
// other connection_name.
func ParseSidecarCredentialConnectionName(name string) (sidecarName, lane string, ok bool) {
	rest, found := strings.CutPrefix(name, sidecarCredentialPrefix)
	if !found {
		return "", "", false
	}
	sidecarName, lane, found = strings.Cut(rest, "/")
	if !found || sidecarName == "" || lane == "" {
		return "", "", false
	}
	return sidecarName, lane, true
}

// SidecarCredential is a sidecar lane credential joined with the user who
// minted it, as the sidecar needs it: the user's identity and the material to
// verify the key, never the key itself.
type SidecarCredential struct {
	ID             string                `gorm:"column:id"`
	ConnectionName string                `gorm:"column:connection_name"`
	SecretKeyHash  string                `gorm:"column:secret_key_hash"`
	UserID         string                `gorm:"column:user_id"`
	UserSubject    string                `gorm:"column:user_subject"`
	UserEmail      string                `gorm:"column:user_email"`
	UserGroups     pq.StringArray        `gorm:"column:user_groups;type:text[]"`
	CreatedAt      time.Time             `gorm:"column:created_at"`
	ExpireAt       time.Time             `gorm:"column:expire_at"`
	RevokedAt      *time.Time            `gorm:"column:revoked_at"`
	ScramSHA256    *daemon.SCRAMMaterial `gorm:"column:scram_sha256;type:jsonb;serializer:json"`
}

// Lane is the listener the credential was minted for.
func (c SidecarCredential) Lane() string {
	_, lane, _ := ParseSidecarCredentialConnectionName(c.ConnectionName)
	return lane
}

const sidecarCredentialSelect = `
	SELECT cc.id, cc.connection_name, cc.secret_key_hash, cc.created_at, cc.expire_at, cc.revoked_at,
		cc.scram_sha256,
		u.id AS user_id, u.subject AS user_subject, u.email AS user_email,
		COALESCE((SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug WHERE ug.user_id = u.id),
			ARRAY[]::TEXT[]) AS user_groups
	FROM private.connection_credentials cc
	JOIN private.users u ON u.org_id = cc.org_id AND u.id::TEXT = cc.user_subject
	WHERE cc.org_id = ? AND cc.connection_type = ?`

// ListValidSidecarCredentials returns the credentials a sidecar verifies a
// client login against: every non-revoked, unexpired credential minted for
// one of its lanes by a user who is still active.
//
// The rows are keyed by the sidecar's NAME, and a name can be deleted and
// made again. Rows older than the sidecar were minted for its namesake, so
// they are left out: a new sidecar never inherits a key nobody minted for
// it.
//
// Both time columns are timestamp WITHOUT time zone holding UTC, so the
// bound instants are UTC wall clocks, as in ListActiveCredentialsByUser.
func ListValidSidecarCredentials(db *gorm.DB, orgID string, sc *Sidecar) ([]SidecarCredential, error) {
	var rows []SidecarCredential
	err := db.Raw(sidecarCredentialSelect+`
		AND u.status = 'active'
		AND cc.connection_name LIKE ? ESCAPE '\'
		AND cc.revoked_at IS NULL
		AND cc.expire_at > ?
		AND cc.created_at >= ?
	ORDER BY cc.created_at`,
		orgID, SidecarCredentialType, likeLiteral(sidecarCredentialPrefix+sc.Name+"/")+"%",
		time.Now().UTC(), sc.CreatedAt.UTC()).Scan(&rows).Error
	return rows, err
}

// ListSidecarCredentialsByUser returns every non-revoked, unexpired sidecar
// lane credential one active user minted, across sidecars.
func ListSidecarCredentialsByUser(db *gorm.DB, orgID, userID string) ([]SidecarCredential, error) {
	var rows []SidecarCredential
	err := db.Raw(sidecarCredentialSelect+`
		AND u.status = 'active'
		AND cc.user_subject = ?
		AND cc.revoked_at IS NULL
		AND cc.expire_at > ?
	ORDER BY cc.created_at DESC`,
		orgID, SidecarCredentialType, userID, time.Now().UTC()).Scan(&rows).Error
	return rows, err
}

// GetSidecarCredential returns one sidecar lane credential, revoked or not.
func GetSidecarCredential(db *gorm.DB, orgID, id string) (*SidecarCredential, error) {
	var rows []SidecarCredential
	err := db.Raw(sidecarCredentialSelect+` AND cc.id = ?`, orgID, SidecarCredentialType, id).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &rows[0], nil
}

// RevokeSidecarCredential marks one sidecar lane credential revoked. A
// sidecar stops serving it on its next handshake. gorm.ErrRecordNotFound
// when no live credential has the id.
func RevokeSidecarCredential(db *gorm.DB, orgID, id string) error {
	now := time.Now().UTC()
	res := db.Table("private.connection_credentials").
		Where("org_id = ? AND id = ? AND connection_type = ? AND revoked_at IS NULL", orgID, id, SidecarCredentialType).
		Updates(map[string]any{"revoked_at": now, "expire_at": now.Add(-time.Hour)})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// likeLiteral escapes s for a LIKE pattern with ESCAPE '\'.
func likeLiteral(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
