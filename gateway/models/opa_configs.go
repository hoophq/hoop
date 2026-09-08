package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type OPAConfig struct {
	ID         string    `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID      string    `gorm:"column:org_id"`
	Name       string    `gorm:"column:name"`
	URL        string    `gorm:"column:url"`
	TimeoutSec int       `gorm:"column:timeout_sec"`
	FailOpen   bool      `gorm:"column:fail_open"`
	Gate       bool      `gorm:"column:gate"`
	CreatedBy  string    `gorm:"column:created_by"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
	// Connections is derived, not stored: the names of the connections whose
	// opa_config_id points here. It is what makes a delete refusal name the
	// connections still using this endpoint.
	Connections pq.StringArray `gorm:"column:connections;type:text[];->"`
}

const opaConfigColumns = `
	o.id, o.org_id, o.name, o.url, o.timeout_sec, o.fail_open, o.gate,
	o.created_by, o.created_at, o.updated_at,
	COALESCE((
		SELECT array_agg(c.name::TEXT) FROM private.connections c
		WHERE c.opa_config_id = o.id
	), ARRAY[]::TEXT[]) AS connections`

func CreateOPAConfig(db *gorm.DB, o *OPAConfig) error {
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	o.CreatedAt = time.Now().UTC()
	o.UpdatedAt = o.CreatedAt
	err := db.Table("private.opa_configs").Create(map[string]any{
		"id":          o.ID,
		"org_id":      o.OrgID,
		"name":        o.Name,
		"url":         o.URL,
		"timeout_sec": o.TimeoutSec,
		"fail_open":   o.FailOpen,
		"gate":        o.Gate,
		"created_by":  o.CreatedBy,
		"created_at":  o.CreatedAt,
		"updated_at":  o.UpdatedAt,
	}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func ListOPAConfigs(db *gorm.DB, orgID string) ([]OPAConfig, error) {
	var items []OPAConfig
	err := db.Raw(`
	SELECT`+opaConfigColumns+`
	FROM private.opa_configs o
	WHERE o.org_id = ?
	ORDER BY o.name`, orgID).
		Find(&items).
		Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

func GetOPAConfigByNameOrID(db *gorm.DB, orgID, nameOrID string) (*OPAConfig, error) {
	identifierClause := "o.name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "o.id = ?"
	}

	var item OPAConfig
	err := db.Raw(`
	SELECT`+opaConfigColumns+`
	FROM private.opa_configs o
	WHERE o.org_id = ? AND `+identifierClause, orgID, nameOrID).
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

// UpdateOPAConfigByNameOrID returns the id of the row it changed, so the
// caller can read the result back by a key the update itself cannot move.
//
// The identifier is resolved to an id first and the write goes through gorm
// rather than a RETURNING statement: gorm's TranslateError only reaches the
// callback pipeline, so a raw UPDATE reports a unique-violation as a driver
// error instead of gorm.ErrDuplicatedKey and the caller loses its 409.
func UpdateOPAConfigByNameOrID(db *gorm.DB, orgID, nameOrID string, o *OPAConfig) (string, error) {
	identifierClause := "name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "id = ?"
	}

	var updatedID string
	err := db.Raw(`
	SELECT id FROM private.opa_configs
	WHERE org_id = ? AND `+identifierClause, orgID, nameOrID).
		Scan(&updatedID).
		Error
	if err != nil {
		return "", err
	}
	if updatedID == "" {
		return "", ErrNotFound
	}

	res := db.Table("private.opa_configs").
		Where("org_id = ? AND id = ?", orgID, updatedID).
		Updates(map[string]any{
			"name":        o.Name,
			"url":         o.URL,
			"timeout_sec": o.TimeoutSec,
			"fail_open":   o.FailOpen,
			"gate":        o.Gate,
			"updated_at":  time.Now().UTC(),
		})
	if res.Error != nil {
		if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
			return "", ErrAlreadyExists
		}
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", ErrNotFound
	}
	return updatedID, nil
}

func DeleteOPAConfigByID(db *gorm.DB, orgID, id string) error {
	res := db.Exec(`
	DELETE FROM private.opa_configs
	WHERE org_id = ? AND id = ?`, orgID, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListOPAConfigsByIDs is the batched lookup the sidecar configuration build
// uses. It does not select the derived connections column.
func ListOPAConfigsByIDs(db *gorm.DB, orgID string, ids []string) (map[string]OPAConfig, error) {
	result := map[string]OPAConfig{}
	if len(ids) == 0 {
		return result, nil
	}
	var items []OPAConfig
	err := db.Raw(`
	SELECT o.id, o.org_id, o.name, o.url, o.timeout_sec, o.fail_open, o.gate
	FROM private.opa_configs o
	WHERE o.org_id = ? AND o.id = ANY(?)`, orgID, pq.Array(ids)).
		Find(&items).
		Error
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		result[item.ID] = item
	}
	return result, nil
}
