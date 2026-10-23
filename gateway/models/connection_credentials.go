package models

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type ConnectionCredentials struct {
	ID                 string     `gorm:"column:id"`
	OrgID              string     `gorm:"column:org_id"`
	UserSubject        string     `gorm:"column:user_subject"`
	ConnectionName     string     `gorm:"column:connection_name"`
	ConnectionType     string     `gorm:"column:connection_type"`
	SecretKeyHash      string     `gorm:"column:secret_key_hash"`
	SecretKey          *string    `gorm:"column:secret_key"`
	EncryptedSecretKey []byte     `gorm:"column:encrypted_secret_key"`
	SessionID          string     `gorm:"column:session_id"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	ExpireAt           time.Time  `gorm:"column:expire_at"`
	RevokedAt          *time.Time `gorm:"column:revoked_at"`
	// ScramSHA256 is the SCRAM-SHA-256 verifier of a sidecar lane credential,
	// computed at mint. Nil on every other credential.
	ScramSHA256 *daemon.SCRAMMaterial `gorm:"column:scram_sha256;type:jsonb;serializer:json"`
}

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

// InsertConnectionCredentials stores a credential row. An empty SessionID is
// stored as NULL: (org_id, session_id) is unique, and a sidecar lane
// credential has no session.
func InsertConnectionCredentials(db *gorm.DB, cred *ConnectionCredentials) error {
	q := db.Table("private.connection_credentials")
	if cred.SessionID == "" {
		q = q.Omit("session_id")
	}
	return q.Create(cred).Error
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
// one of its lanes.
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
// lane credential one user minted, across sidecars.
func ListSidecarCredentialsByUser(db *gorm.DB, orgID, userID string) ([]SidecarCredential, error) {
	var rows []SidecarCredential
	err := db.Raw(sidecarCredentialSelect+`
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

func CreateConnectionCredentials(db *ConnectionCredentials) (*ConnectionCredentials, error) {
	return db, DB.Table("private.connection_credentials").Create(db).Error
}

func GetConnectionCredentialsByID(orgID, id string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND id = ?", orgID, id).
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &resp, err
}

// GetValidConnectionCredentialsBySecretKey retrieves a valid connection credential by its secret key hash.
// if a user has a valid connection credential, it could be used to connect in the requested resource
func GetValidConnectionCredentialsBySecretKey(connectionTypes []string, secretKeyHash string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("connection_type IN ? AND secret_key_hash = ?", connectionTypes, secretKeyHash).
		Order("created_at DESC").
		First(&resp).
		Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}

		return nil, err
	}

	if resp.RevokedAt != nil {
		return nil, ErrNotFound
	}

	return &resp, err
}

// GetConnectionByTypeAndID retrieves a connection credential by its type and ID
func GetConnectionByTypeAndID(connectionType, id string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("connection_type = ? AND id = ?", connectionType, id).
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &resp, err
}

// GetConnectionCredentialsBySessionID retrieves a connection credential by session ID
func GetConnectionCredentialsBySessionID(orgID, sessionID string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND session_id = ?", orgID, sessionID).
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &resp, err
}

// GetActiveCredentialByUserAndConnection returns the non-revoked credential for the
// given (org, user, connection) triple. Callers use this to implement stable-key
// reuse: the plaintext secret key is read from the secret_key column (falling
// back to the legacy encrypted_secret_key copy) and the expiration is refreshed
// in place.
// ErrNotFound is returned when the user does not yet have a credential for the
// connection or when the prior credential has been revoked.
func GetActiveCredentialByUserAndConnection(orgID, userSubject, connectionName string) (*ConnectionCredentials, error) {
	var resp ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND user_subject = ? AND connection_name = ? AND revoked_at IS NULL",
			orgID, userSubject, connectionName).
		Order("created_at DESC").
		First(&resp).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &resp, err
}

// ActiveConnectionCredential is a secret-less projection of a credential row
// joined with its connection. The secret columns (secret_key, secret_key_hash,
// encrypted_secret_key) are deliberately absent from the struct so they cannot
// be selected or serialised by accident.
type ActiveConnectionCredential struct {
	ID                string    `gorm:"column:id"`
	ConnectionID      string    `gorm:"column:connection_id"`
	ConnectionName    string    `gorm:"column:connection_name"`
	ConnectionType    string    `gorm:"column:connection_type"`
	ConnectionSubType string    `gorm:"column:connection_subtype"`
	SessionID         string    `gorm:"column:session_id"`
	CreatedAt         time.Time `gorm:"column:created_at"`
	ExpireAt          time.Time `gorm:"column:expire_at"`
}

// ListActiveCredentialsByUser returns every non-revoked, unexpired credential
// owned by the given user, joined with its connection so callers get the
// connection's public type/subtype — the credential row stores the collapsed
// proto type instead (e.g. "httpproxy" for a grafana connection).
//
// The inner join drops credentials whose connection was deleted; those are
// unusable anyway. The expire_at predicate keeps persistent credentials, whose
// expiration is the far-future sentinel.
//
// Note this does not apply the access-control-group join that
// ListConnectionsPaginated does: a credential the user minted themselves stays
// listed even if their group access is later revoked. That mirrors the proxy,
// where revocation is an explicit action on the credential.
func ListActiveCredentialsByUser(db *gorm.DB, orgID, userSubject string) ([]ActiveConnectionCredential, error) {
	// DISTINCT ON collapses the row to one per connection. Several can be live
	// at once — Create reuses the existing credential but Resume mints a
	// parallel one — and callers key this by connection name, so without a rule
	// here the winner was whichever the client's reducer happened to keep. The
	// order inside the group is the same rule the rest of the code resolves by:
	// the credential still attached to a session first, then the newest.
	//
	// expire_at is compared against a bound instant rather than SQL NOW(): the
	// column is timestamp WITHOUT time zone holding UTC, so NOW() would compare
	// it against the server's local wall clock and answer a different question
	// than every Go-side expiry check.
	var rows []ActiveConnectionCredential
	err := db.Raw(`
		SELECT * FROM (
			SELECT DISTINCT ON (cc.connection_name)
				cc.id                       AS id,
				c.id                        AS connection_id,
				cc.connection_name          AS connection_name,
				c.type                      AS connection_type,
				COALESCE(c.subtype, '')     AS connection_subtype,
				COALESCE(cc.session_id, '') AS session_id,
				cc.created_at               AS created_at,
				cc.expire_at                AS expire_at
			FROM private.connection_credentials cc
			INNER JOIN private.connections c
				ON c.org_id = cc.org_id AND c.name = cc.connection_name
			WHERE cc.org_id = ?
				AND cc.user_subject = ?
				AND cc.revoked_at IS NULL
				AND cc.expire_at > ?
			ORDER BY cc.connection_name, (cc.session_id IS NOT NULL) DESC, cc.created_at DESC
		) t
		ORDER BY t.created_at DESC
	`, orgID, userSubject, time.Now().UTC()).Scan(&rows).Error
	return rows, err
}

// UpdateConnectionCredentialsSecret rotates the secret of an existing credential,
// storing both the hash (proxy auth lookup) and the plaintext (stable-key reuse).
// The legacy encrypted copy is cleared since it no longer matches the new secret.
func UpdateConnectionCredentialsSecret(id, secretKeyHash, secretKey string) error {
	return DB.Table("private.connection_credentials").
		Where("id = ?", id).
		Updates(map[string]any{
			"secret_key_hash":      secretKeyHash,
			"secret_key":           secretKey,
			"encrypted_secret_key": nil,
		}).Error
}

// BackfillConnectionCredentialsSecretKey stores the plaintext secret key on a
// row that predates plaintext storage (recovered from the legacy encrypted
// copy). Hash and encrypted copy are left untouched.
func BackfillConnectionCredentialsSecretKey(id, secretKey string) error {
	return DB.Table("private.connection_credentials").
		Where("id = ?", id).
		Update("secret_key", secretKey).Error
}

// RefreshCredentialExpiration updates the expiration and session_id of an
// existing credential. Used for stable-key reuse: the row keeps its id and
// stored secret key while each issuance refreshes the audit session and the
// validity window.
func RefreshCredentialExpiration(id, sessionID string, expireAt time.Time) error {
	return DB.Table("private.connection_credentials").
		Where("id = ?", id).
		Updates(map[string]any{
			"session_id": sessionID,
			"expire_at":  expireAt,
		}).Error
}

// ClearCredentialSession unlinks the credential from its audit session without
// invalidating the credential itself. Used by the "close session" endpoint so
// the user can reconnect later with the same token while the prior audit
// session is finalised.
func ClearCredentialSession(id string) error {
	return DB.Table("private.connection_credentials").
		Where("id = ?", id).
		Update("session_id", nil).Error
}

// CloseExpiredCredentialSessions finalises the audit session of every
// credential whose access window has elapsed, and unlinks the credential from
// it. The credential row itself is preserved (with its stored secret key) so a
// subsequent CreateConnectionCredentials call can reuse the same token by
// re-arming expire_at and session_id via RefreshCredentialExpiration.
//
// It returns the number of credentials swept. Callers are expected to be the
// background sweeper (gateway/jobs/credentialsweeper) — this used to run inline
// on GET /sessions and the credential endpoints, where a read scoped to one org
// issued 1+2N serial statements closing sessions for every tenant on the
// deployment.
//
// Everything happens in one statement:
//
//   - FOR UPDATE SKIP LOCKED so concurrent gateway replicas each take a
//     disjoint slice instead of contending on the same rows.
//   - LIMIT so a backlog drains over several ticks rather than in one long
//     transaction that pins the xmin horizon.
//   - `s.status <> 'done'` keeps it idempotent and stops it overwriting an
//     ended_at written by a real close (e.g. the revoke path, which pushes
//     expire_at into the past without clearing session_id).
//   - session_id is text while sessions.id is uuid, so the join needs the
//     explicit cast; casting the other way would forfeit the sessions primary
//     key index and seq scan the largest table in the schema.
//
// expire_at is compared against a bound instant rather than SQL NOW(): the
// column is timestamp WITHOUT time zone holding UTC, so NOW() would compare it
// against the server's local wall clock and answer a different question than
// every Go-side expiry check. Same reasoning as ListActiveCredentialsByUser.
func CloseExpiredCredentialSessions(ctx context.Context, db *gorm.DB, limit int) (int64, error) {
	res := db.WithContext(ctx).Exec(`
	WITH expired AS (
		SELECT id, org_id, session_id
		FROM private.connection_credentials
		WHERE expire_at < @now AND session_id IS NOT NULL AND session_id != ''
		ORDER BY expire_at
		LIMIT @limit
		FOR UPDATE SKIP LOCKED
	), closed AS (
		UPDATE private.sessions s
		SET status = 'done', ended_at = @now
		FROM expired e
		WHERE s.org_id = e.org_id AND s.id = e.session_id::uuid AND s.status <> 'done'
	)
	UPDATE private.connection_credentials cc
	SET session_id = NULL
	FROM expired e
	WHERE cc.id = e.id`,
		map[string]any{"now": time.Now().UTC(), "limit": limit})
	return res.RowsAffected, res.Error
}

// RevokeConnectionCredentials marks a credential as revoked. Because the
// stable-password contract can leave several rows sharing the same
// secret_key_hash (e.g. a review/Resume row plus the persistent row for the
// same user and connection), revocation burns the password itself: every
// non-revoked row with the same hash is marked, so neither proxy auth
// (GetValidConnectionCredentialsBySecretKey) nor stable-key reuse
// (GetActiveCredentialByUserAndConnection) can resurrect it. Revoked rows are
// kept for forensic queries. The next CreateConnectionCredentials call for the
// same (user, connection) generates a fresh row with a new key.
func RevokeConnectionCredentials(orgID, credentialID string) error {
	now := time.Now().UTC()
	return DB.Table("private.connection_credentials").
		Where(`org_id = ? AND revoked_at IS NULL AND secret_key_hash = (
			SELECT secret_key_hash FROM private.connection_credentials WHERE org_id = ? AND id = ?)`,
			orgID, orgID, credentialID).
		Updates(map[string]any{
			"revoked_at": now,
			// Also push expire_at into the past so any proxy that still reads
			// the row via the legacy expire_at check rejects new connections
			// immediately.
			"expire_at": now.Add(-time.Hour),
		}).Error
}

// ListConnectionCredentialsNotRevokedBySecretKeyHash returns every credential row sharing
// the given secret key hash that has not been revoked. Used by revocation to tear down in-flight proxy
// sessions across all rows that share the burned password.
func ListNonRevokedConnectionCredentialsBySecretKeyHash(orgID, secretKeyHash string) ([]*ConnectionCredentials, error) {
	var resp []*ConnectionCredentials
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND secret_key_hash = ? AND revoked_at IS NULL", orgID, secretKeyHash).
		Find(&resp).Error
	return resp, err
}

// HasRevokedCredentialWithHash reports whether any credential row carrying the
// given secret key hash has been revoked. Reuse paths call this to refuse
// re-issuing a burned password: revocation marks every sibling row sharing the
// hash, but rows revoked by the legacy single-row revoke can leave a
// non-revoked sibling still holding the dead password.
func HasRevokedCredentialWithHash(orgID, secretKeyHash string) (bool, error) {
	var count int64
	err := DB.Table("private.connection_credentials").
		Where("org_id = ? AND secret_key_hash = ? AND revoked_at IS NOT NULL", orgID, secretKeyHash).
		Count(&count).Error
	return count > 0, err
}
