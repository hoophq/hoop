package models

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SidecarServiceAccount lets the holders of one platform service account
// token reach a sidecar by name. The plane verifies a JWT from
// Issuer for Audience, reads Claim, matches it against SubjectPattern (exact,
// or one '*') and renders NameTemplate, where "{1}" is the text '*' matched.
//
// JWKS, when set, is the issuer's key set and nothing is fetched; nil means
// OIDC discovery at {Issuer}/.well-known/openid-configuration.
type SidecarServiceAccount struct {
	ID              string          `gorm:"column:id"`
	OrgID           string          `gorm:"column:org_id"`
	Name            string          `gorm:"column:name"`
	Issuer          string          `gorm:"column:issuer"`
	Audience        string          `gorm:"column:audience"`
	Claim           string          `gorm:"column:claim"`
	SubjectPattern  string          `gorm:"column:subject_pattern"`
	NameTemplate    string          `gorm:"column:name_template"`
	JWKS            json.RawMessage `gorm:"column:jwks"`
	AllowAnySubject bool            `gorm:"column:allow_any_subject"`
	CreatedBy       string          `gorm:"column:created_by"`
	CreatedAt       time.Time       `gorm:"column:created_at"`
	UpdatedAt       time.Time       `gorm:"column:updated_at"`
}

const sidecarServiceAccountColumns = `
	id, org_id, name, issuer, audience, claim, subject_pattern, name_template,
	jwks, allow_any_subject, created_by, created_at, updated_at`

// jwksParam passes an absent key set as SQL NULL. An empty RawMessage is not
// a JSON document, and the column must read back as nil.
func jwksParam(jwks json.RawMessage) any {
	if len(jwks) == 0 {
		return nil
	}
	return string(jwks)
}

func ListSidecarServiceAccounts(db *gorm.DB, orgID string) ([]SidecarServiceAccount, error) {
	var items []SidecarServiceAccount
	err := db.Raw(`
	SELECT`+sidecarServiceAccountColumns+`
	FROM private.sidecar_service_accounts
	WHERE org_id = ?
	ORDER BY name`, orgID).
		Scan(&items).
		Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// ListSidecarServiceAccountsByIssuer returns the mappings of every
// organization for one exact issuer. It is not org scoped: the token names
// its issuer and nothing else, and a match in two organizations is refused by
// the caller. Oldest first, the tie-break the caller applies.
func ListSidecarServiceAccountsByIssuer(db *gorm.DB, issuer string) ([]SidecarServiceAccount, error) {
	var items []SidecarServiceAccount
	err := db.Raw(`
	SELECT`+sidecarServiceAccountColumns+`
	FROM private.sidecar_service_accounts
	WHERE issuer = ?
	ORDER BY created_at, id`, issuer).
		Scan(&items).
		Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

// GetSidecarServiceAccount returns ErrNotFound when no row matched, including
// for an id that is not a UUID.
func GetSidecarServiceAccount(db *gorm.DB, orgID, id string) (*SidecarServiceAccount, error) {
	var item SidecarServiceAccount
	err := db.Raw(`
	SELECT`+sidecarServiceAccountColumns+`
	FROM private.sidecar_service_accounts
	WHERE org_id = ? AND id::TEXT = ?`, orgID, id).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrNotFound
	}
	return &item, nil
}

// ErrSidecarServiceAccountPairTaken refuses a mapping whose issuer and
// audience another organization already uses. On a multi-tenant plane every
// organization shares the URL sidecars send as audience, so a second
// organization mapping the same pair could match the first one's subjects:
// its sidecars would then be refused as ambiguous, or enroll into the wrong
// organization.
var ErrSidecarServiceAccountPairTaken = errors.New("this issuer and audience are already used by another organization; " +
	"use an audience unique to your organization (for example the control plane URL with a path naming your organization)")

// claimIssuerAudience holds the (issuer, audience) pair for orgID until tx
// ends, and returns ErrSidecarServiceAccountPairTaken when another
// organization has a mapping with it. The pair may be absent, so no row can
// be locked: a transaction advisory lock on the pair serializes the check
// and the write of concurrent writers instead. Two keys, one hash each,
// because text cannot hold the NUL a single concatenated key would need as
// a separator; a hash collision only serializes two unrelated writes.
func claimIssuerAudience(tx *gorm.DB, orgID, issuer, audience string) error {
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?), hashtext(?))`, issuer, audience).Error; err != nil {
		return err
	}
	var taken bool
	err := tx.Raw(`
	SELECT EXISTS (
		SELECT 1 FROM private.sidecar_service_accounts
		WHERE issuer = ? AND audience = ? AND org_id <> ?)`, issuer, audience, orgID).
		Scan(&taken).
		Error
	if err != nil {
		return err
	}
	if taken {
		return ErrSidecarServiceAccountPairTaken
	}
	return nil
}

// CreateSidecarServiceAccount stores sa and fills it with the stored row.
// Returns ErrAlreadyExists when the name, or the issuer, claim and pattern,
// are already used in the organization, and
// ErrSidecarServiceAccountPairTaken when another organization uses the
// issuer and audience.
//
// An Exec and a read back in one transaction, rather than INSERT ...
// RETURNING through Scan: a constraint error that surfaces while rows are
// read is not translated to gorm.ErrDuplicatedKey, so a conflict would read
// as a 500.
func CreateSidecarServiceAccount(db *gorm.DB, sa *SidecarServiceAccount) error {
	id := uuid.NewString()
	var created *SidecarServiceAccount
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := claimIssuerAudience(tx, sa.OrgID, sa.Issuer, sa.Audience); err != nil {
			return err
		}
		err := tx.Exec(`
		INSERT INTO private.sidecar_service_accounts (id, org_id, name, issuer, audience, claim,
			subject_pattern, name_template, jwks, allow_any_subject, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?)`,
			id, sa.OrgID, sa.Name, sa.Issuer, sa.Audience, sa.Claim, sa.SubjectPattern,
			sa.NameTemplate, jwksParam(sa.JWKS), sa.AllowAnySubject, sa.CreatedBy).Error
		if err != nil {
			return err
		}
		created, err = GetSidecarServiceAccount(tx, sa.OrgID, id)
		return err
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrAlreadyExists
		}
		return err
	}
	*sa = *created
	return nil
}

// UpdateSidecarServiceAccount replaces every field an admin writes and
// returns the stored row. Returns ErrNotFound when no row matched, and
// ErrAlreadyExists and ErrSidecarServiceAccountPairTaken as the create does.
func UpdateSidecarServiceAccount(db *gorm.DB, sa *SidecarServiceAccount) (*SidecarServiceAccount, error) {
	var updated *SidecarServiceAccount
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := claimIssuerAudience(tx, sa.OrgID, sa.Issuer, sa.Audience); err != nil {
			return err
		}
		res := tx.Exec(`
		UPDATE private.sidecar_service_accounts
		SET name = ?, issuer = ?, audience = ?, claim = ?, subject_pattern = ?, name_template = ?,
			jwks = ?::jsonb, allow_any_subject = ?, updated_at = NOW()
		WHERE org_id = ? AND id::TEXT = ?`,
			sa.Name, sa.Issuer, sa.Audience, sa.Claim, sa.SubjectPattern, sa.NameTemplate,
			jwksParam(sa.JWKS), sa.AllowAnySubject, sa.OrgID, sa.ID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		var err error
		updated, err = GetSidecarServiceAccount(tx, sa.OrgID, sa.ID)
		return err
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return nil, ErrAlreadyExists
	}
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteSidecarServiceAccount removes the mapping. The sidecars it reached
// stay; their identities stop authenticating unless another mapping allows
// them. Returns ErrNotFound when no row matched.
func DeleteSidecarServiceAccount(db *gorm.DB, orgID, id string) error {
	res := db.Exec(`
	DELETE FROM private.sidecar_service_accounts
	WHERE org_id = ? AND id::TEXT = ?`, orgID, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
