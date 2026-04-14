package models

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type APIKey struct {
	ID            string         `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID         string         `gorm:"column:org_id"`
	Name          string         `gorm:"column:name"`
	KeyHash       string         `gorm:"column:key_hash"`
	MaskedKey     string         `gorm:"column:masked_key"`
	Status        string         `gorm:"column:status"`
	Groups        pq.StringArray `gorm:"column:groups;type:text[];->"`
	CreatedBy     string         `gorm:"column:created_by"`
	DeactivatedBy *string        `gorm:"column:deactivated_by"`
	CreatedAt     time.Time      `gorm:"column:created_at"`
	DeactivatedAt *time.Time     `gorm:"column:deactivated_at"`
	LastUsedAt    *time.Time     `gorm:"column:last_used_at"`
}

func HashAPIKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("%x", h)
}

// MaskAPIKey masks a raw API key, keeping a short prefix visible.
// For a key like "sk-ant-api03-VgZiIKFLxxxx...xxxx", it keeps
// the first 15 characters and replaces the rest with "*******".
// Keys shorter than 20 characters keep only the first 4 characters visible.
func MaskAPIKey(rawKey string) string {
	if len(rawKey) < 20 {
		if len(rawKey) <= 4 {
			return strings.Repeat("*", len(rawKey))
		}
		return rawKey[:4] + strings.Repeat("*", len(rawKey)-4)
	}
	return rawKey[:15] + "*******"
}

func ListAPIKeys(orgID string) ([]APIKey, error) {
	var items []APIKey
	return items, DB.Raw(`
	SELECT ak.id, ak.org_id, ak.name, ak.masked_key, ak.status,
	ak.created_by, ak.deactivated_by, ak.created_at, ak.deactivated_at, ak.last_used_at,
	COALESCE((
		SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
		WHERE ug.api_key_id = ak.id
	), ARRAY[]::TEXT[]) AS groups
	FROM private.api_keys ak
	WHERE ak.org_id = ?`, orgID).
		Find(&items).
		Error
}

func GetAPIKeyByNameOrID(orgID, nameOrID string) (*APIKey, error) {
	var item APIKey
	identifierClause := "ak.name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "ak.id = ?"
	}

	err := DB.Raw(`
	SELECT ak.id, ak.org_id, ak.name, ak.masked_key, ak.status,
	ak.created_by, ak.deactivated_by, ak.created_at, ak.deactivated_at, ak.last_used_at,
	COALESCE((
		SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug
		WHERE ug.api_key_id = ak.id
	), ARRAY[]::TEXT[]) AS groups
	FROM private.api_keys ak
	WHERE ak.org_id = ? AND `+identifierClause, orgID, nameOrID).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, nil
	}
	return &item, nil
}

func CreateAPIKey(apiKey *APIKey) error {
	if apiKey.ID == "" {
		apiKey.ID = uuid.NewString()
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Table("private.api_keys").Create(map[string]any{
			"id":         apiKey.ID,
			"org_id":     apiKey.OrgID,
			"name":       apiKey.Name,
			"key_hash":   apiKey.KeyHash,
			"masked_key": apiKey.MaskedKey,
			"status":     apiKey.Status,
			"created_by": apiKey.CreatedBy,
		}).Error
		if err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return ErrAlreadyExists
			}
			return err
		}
		for _, group := range apiKey.Groups {
			err = tx.Exec(`
			INSERT INTO private.user_groups (org_id, api_key_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, apiKey.OrgID, apiKey.ID, group).
				Error
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func UpdateAPIKey(apiKey *APIKey) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Table("private.api_keys").
			Where("id = ? AND org_id = ? AND status = 'active'", apiKey.ID, apiKey.OrgID).
			Updates(map[string]any{
				"name": apiKey.Name,
			})
		if res.Error != nil {
			if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
				return ErrAlreadyExists
			}
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		err := tx.Exec(`
			DELETE FROM private.user_groups
			WHERE org_id = ? AND api_key_id = ?`, apiKey.OrgID, apiKey.ID).
			Error
		if err != nil {
			return fmt.Errorf("failed to delete api key groups: %v", err)
		}
		for _, group := range apiKey.Groups {
			err = tx.Exec(`
			INSERT INTO private.user_groups (org_id, api_key_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, apiKey.OrgID, apiKey.ID, group).
				Error
			if err != nil {
				return fmt.Errorf("failed to insert api key group: %v", err)
			}
		}
		return nil
	})
}

func RevokeAPIKey(orgID, id, deactivatedBy string) error {
	res := DB.Table("private.api_keys").
		Where("id = ? AND org_id = ? AND status = 'active'", id, orgID).
		Updates(map[string]any{
			"status":         "revoked",
			"deactivated_by": deactivatedBy,
			"deactivated_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
