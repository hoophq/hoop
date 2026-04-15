package models

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

const tableAPIKeysConnections = "private.api_keys_connections"

type APIKey struct {
	ID            string         `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID         string         `gorm:"column:org_id"`
	Name          string         `gorm:"column:name"`
	KeyHash       string         `gorm:"column:key_hash"`
	MaskedKey     string         `gorm:"column:masked_key"`
	Status        string         `gorm:"column:status"`
	Groups        pq.StringArray `gorm:"column:groups;type:text[];->"`
	ConnectionIDs []string       `gorm:"-"`
	CreatedBy     string         `gorm:"column:created_by"`
	DeactivatedBy *string        `gorm:"column:deactivated_by"`
	CreatedAt     time.Time      `gorm:"column:created_at"`
	DeactivatedAt *time.Time     `gorm:"column:deactivated_at"`
	LastUsedAt    *time.Time     `gorm:"column:last_used_at"`
}

type APIKeyConnection struct {
	ID           string    `gorm:"column:id"`
	OrgID        string    `gorm:"column:org_id"`
	APIKeyID     string    `gorm:"column:api_key_id"`
	ConnectionID string    `gorm:"column:connection_id"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func GenerateAPIKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate random key: " + err.Error())
	}
	return "hpk_" + base64.RawURLEncoding.EncodeToString(b)
}

func HashAPIKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("%x", h)
}

func MaskAPIKey(rawKey string) string {
	if len(rawKey) <= 8 {
		return strings.Repeat("*", len(rawKey))
	}
	return rawKey[:8] + strings.Repeat("*", len(rawKey)-8)
}

func ListAPIKeys(orgID string) ([]APIKey, error) {
	var items []APIKey
	err := DB.Raw(`
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
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	if len(ids) == 0 {
		return items, nil
	}

	var connections []struct {
		APIKeyID     string `gorm:"column:api_key_id"`
		ConnectionID string `gorm:"column:connection_id"`
	}
	err = DB.Table(tableAPIKeysConnections).
		Select("api_key_id, connection_id").
		Where("org_id = ? AND api_key_id IN (?)", orgID, ids).
		Scan(&connections).Error
	if err != nil {
		return nil, err
	}

	connMap := make(map[string][]string)
	for _, c := range connections {
		connMap[c.APIKeyID] = append(connMap[c.APIKeyID], c.ConnectionID)
	}
	for i := range items {
		items[i].ConnectionIDs = connMap[items[i].ID]
	}

	return items, nil
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

	var connectionIDs []string
	err = DB.Table(tableAPIKeysConnections).
		Select("connection_id").
		Where("org_id = ? AND api_key_id = ?", orgID, item.ID).
		Pluck("connection_id", &connectionIDs).Error
	if err != nil {
		return nil, err
	}
	item.ConnectionIDs = connectionIDs

	return &item, nil
}

func CreateAPIKey(apiKey *APIKey) error {
	if apiKey.ID == "" {
		apiKey.ID = uuid.NewString()
	}
	apiKey.CreatedAt = time.Now().UTC()
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Table("private.api_keys").Create(map[string]any{
			"id":         apiKey.ID,
			"org_id":     apiKey.OrgID,
			"name":       apiKey.Name,
			"key_hash":   apiKey.KeyHash,
			"masked_key": apiKey.MaskedKey,
			"status":     apiKey.Status,
			"created_by": apiKey.CreatedBy,
			"created_at": apiKey.CreatedAt,
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
		validConnectionIDs, err := upsertAPIKeyConnections(tx, apiKey.OrgID, apiKey.ID, apiKey.ConnectionIDs)
		if err != nil {
			return err
		}
		apiKey.ConnectionIDs = validConnectionIDs
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
		validConnectionIDs, err := upsertAPIKeyConnections(tx, apiKey.OrgID, apiKey.ID, apiKey.ConnectionIDs)
		if err != nil {
			return err
		}
		apiKey.ConnectionIDs = validConnectionIDs
		return nil
	})
}

func upsertAPIKeyConnections(tx *gorm.DB, orgID, apiKeyID string, connectionIDs []string) ([]string, error) {
	if err := tx.Table(tableAPIKeysConnections).
		Where("org_id = ? AND api_key_id = ?", orgID, apiKeyID).
		Delete(&APIKeyConnection{}).Error; err != nil {
		return nil, err
	}

	var validIDs []string
	for _, connID := range connectionIDs {
		var conn Connection
		err := tx.Table("private.connections").
			Where("org_id = ? AND id = ?", orgID, connID).
			First(&conn).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		akc := APIKeyConnection{
			ID:           uuid.NewString(),
			OrgID:        orgID,
			APIKeyID:     apiKeyID,
			ConnectionID: conn.ID,
			CreatedAt:    time.Now().UTC(),
		}
		if err := tx.Table(tableAPIKeysConnections).Create(&akc).Error; err != nil {
			return nil, err
		}
		validIDs = append(validIDs, conn.ID)
	}
	return validIDs, nil
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
