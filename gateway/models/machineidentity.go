package models

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MachineIdentity struct {
	ID              string         `gorm:"column:id"`
	OrgID           string         `gorm:"column:org_id"`
	Name            string         `gorm:"column:name"`
	Description     string         `gorm:"column:description"`
	ConnectionNames pq.StringArray `gorm:"column:connection_names;type:text[]"`
	CreatedAt       time.Time      `gorm:"column:created_at"`
	UpdatedAt       time.Time      `gorm:"column:updated_at"`
}

type MachineIdentityCredential struct {
	ID                     string    `gorm:"column:id"`
	OrgID                  string    `gorm:"column:org_id"`
	MachineIdentityID      string    `gorm:"column:machine_identity_id"`
	ConnectionCredentialID string    `gorm:"column:connection_credential_id"`
	ConnectionName         string    `gorm:"column:connection_name"`
	SecretKey              string    `gorm:"column:secret_key"`
	CreatedAt              time.Time `gorm:"column:created_at"`
}

func ListMachineIdentities(orgID string) ([]MachineIdentity, error) {
	var items []MachineIdentity
	return items, DB.Table("private.machine_identities").
		Where("org_id = ?", orgID).
		Order("created_at DESC").
		Find(&items).Error
}

func GetMachineIdentity(orgID, id string) (*MachineIdentity, error) {
	var mi MachineIdentity
	err := DB.Table("private.machine_identities").
		Where("org_id = ? AND id = ?", orgID, id).
		First(&mi).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &mi, err
}

func GetMachineIdentityByName(orgID, name string) (*MachineIdentity, error) {
	var mi MachineIdentity
	err := DB.Table("private.machine_identities").
		Where("org_id = ? AND name = ?", orgID, name).
		First(&mi).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &mi, err
}

func CreateMachineIdentity(mi *MachineIdentity) error {
	return DB.Table("private.machine_identities").Create(mi).Error
}

func UpdateMachineIdentity(mi *MachineIdentity) error {
	res := DB.Table("private.machine_identities").
		Clauses(clause.Returning{}).
		Where("org_id = ? AND id = ?", mi.OrgID, mi.ID).
		Updates(map[string]any{
			"name":             mi.Name,
			"description":      mi.Description,
			"connection_names": mi.ConnectionNames,
			"updated_at":       time.Now().UTC(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMachineIdentity deletes a machine identity row. Credential cleanup
// (revocation + proxy cancellation) must be performed by the caller BEFORE
// calling this function. The DB CASCADE on machine_identity_credentials will
// clean up any remaining junction rows.
func DeleteMachineIdentity(orgID, id string) error {
	res := DB.Exec("DELETE FROM private.machine_identities WHERE org_id = ? AND id = ?", orgID, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func CreateMachineIdentityCredential(mic *MachineIdentityCredential) error {
	return DB.Table("private.machine_identity_credentials").Create(mic).Error
}

func ListMachineIdentityCredentials(orgID, identityID string) ([]MachineIdentityCredential, error) {
	var items []MachineIdentityCredential
	return items, DB.Table("private.machine_identity_credentials").
		Where("org_id = ? AND machine_identity_id = ?", orgID, identityID).
		Find(&items).Error
}

func GetMachineIdentityCredentialByConnName(orgID, identityID, connName string) (*MachineIdentityCredential, error) {
	var mic MachineIdentityCredential
	err := DB.Table("private.machine_identity_credentials").
		Where("org_id = ? AND machine_identity_id = ? AND connection_name = ?", orgID, identityID, connName).
		First(&mic).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &mic, err
}

// RevokedCredentialInfo contains metadata about a revoked credential,
// needed by the service layer to cancel active proxy sessions.
type RevokedCredentialInfo struct {
	CredentialID   string
	ConnectionType string
	SecretKeyHash  string
	SessionID      string
}

// DeleteMachineIdentityCredential revokes and deletes a machine identity credential.
// It performs full DB-level revocation:
// 1. Sets expire_at to the past on connection_credentials (soft invalidation)
// 2. Marks the associated session as done with credentials_revoked_at metadata
// 3. Deletes the connection_credentials row
// 4. Deletes the machine_identity_credentials junction row
//
// Returns credential info needed by the caller to cancel active proxy sessions.
func DeleteMachineIdentityCredential(orgID, identityID, connName string) (*RevokedCredentialInfo, error) {
	mic, err := GetMachineIdentityCredentialByConnName(orgID, identityID, connName)
	if err != nil {
		return nil, err
	}

	cred, err := GetConnectionCredentialsByID(orgID, mic.ConnectionCredentialID)
	if err != nil {
		return nil, fmt.Errorf("failed fetching connection credential: %w", err)
	}

	info := &RevokedCredentialInfo{
		CredentialID:   cred.ID,
		ConnectionType: cred.ConnectionType,
		SecretKeyHash:  cred.SecretKeyHash,
		SessionID:      cred.SessionID,
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		// Soft-invalidate: set expire_at to the past
		if err := tx.Table("private.connection_credentials").
			Where("org_id = ? AND id = ?", orgID, cred.ID).
			Update("expire_at", time.Now().UTC().Add(-time.Hour)).Error; err != nil {
			return fmt.Errorf("failed revoking connection credential: %w", err)
		}

		// Mark session as done
		if cred.SessionID != "" {
			if err := SetSessionCredentialsRevokedAt(orgID, cred.SessionID, time.Now().UTC()); err != nil {
				fmt.Printf("warn: failed setting session credentials revoked_at: %v\n", err)
			}
		}

		// Hard-delete connection_credentials row.
		// The ON DELETE CASCADE on machine_identity_credentials.connection_credential_id
		// automatically deletes the junction row.
		if err := tx.Exec("DELETE FROM private.connection_credentials WHERE id = ?", cred.ID).Error; err != nil {
			return fmt.Errorf("failed deleting connection credential: %w", err)
		}

		return nil
	})

	return info, err
}

// DeleteAllMachineIdentityCredentials revokes and deletes all credentials for a machine identity.
// Returns credential info for each revoked credential so the caller can cancel active proxy sessions.
func DeleteAllMachineIdentityCredentials(orgID, identityID string) ([]*RevokedCredentialInfo, error) {
	micRows, err := ListMachineIdentityCredentials(orgID, identityID)
	if err != nil {
		return nil, err
	}

	var infos []*RevokedCredentialInfo
	for _, mic := range micRows {
		cred, err := GetConnectionCredentialsByID(orgID, mic.ConnectionCredentialID)
		if err != nil {
			continue // credential may already be gone
		}
		infos = append(infos, &RevokedCredentialInfo{
			CredentialID:   cred.ID,
			ConnectionType: cred.ConnectionType,
			SecretKeyHash:  cred.SecretKeyHash,
			SessionID:      cred.SessionID,
		})
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		for _, mic := range micRows {
			// Soft-invalidate
			if err := tx.Table("private.connection_credentials").
				Where("org_id = ? AND id = ?", orgID, mic.ConnectionCredentialID).
				Update("expire_at", time.Now().UTC().Add(-time.Hour)).Error; err != nil {
				fmt.Printf("warn: failed revoking connection credential %s: %v\n", mic.ConnectionCredentialID, err)
			}
		}

		// Mark sessions as done
		for _, info := range infos {
			if info.SessionID != "" {
				if err := SetSessionCredentialsRevokedAt(orgID, info.SessionID, time.Now().UTC()); err != nil {
					fmt.Printf("warn: failed setting session credentials revoked_at for %s: %v\n", info.SessionID, err)
				}
			}
		}

		// Hard-delete connection_credentials rows.
		// The ON DELETE CASCADE on machine_identity_credentials.connection_credential_id
		// automatically deletes the junction rows.
		for _, mic := range micRows {
			if err := tx.Exec("DELETE FROM private.connection_credentials WHERE id = ?", mic.ConnectionCredentialID).Error; err != nil {
				return fmt.Errorf("failed deleting connection credential %s: %w", mic.ConnectionCredentialID, err)
			}
		}

		return nil
	})

	return infos, err
}

func GenerateMachineIdentityID(orgID, name string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(orgID+":machineidentity:"+name)).String()
}

// IsMachineIdentityCredential checks if a connection credential belongs to a machine identity
// by looking up the machine_identity_credentials junction table.
func IsMachineIdentityCredential(connectionCredentialID string) bool {
	var count int64
	DB.Table("private.machine_identity_credentials").
		Where("connection_credential_id = ?", connectionCredentialID).
		Count(&count)
	return count > 0
}
