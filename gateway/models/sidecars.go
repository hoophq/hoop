package models

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/sidecar/daemon"
	"gorm.io/gorm"
)

// SidecarConfiguration is the daemon config document stored in the
// sidecars.configuration JSONB column. The Scanner/Valuer pair keeps the JSON
// encoding at the database edge, so every caller above it reads a typed
// config instead of bytes it has to decode itself.
type SidecarConfiguration daemon.Config

func (c SidecarConfiguration) Value() (driver.Value, error) { return json.Marshal(c) }

// Scan decodes into a fresh value and rejects a key the compiled
// daemon.Config does not declare, the same rule the write path and the
// sidecar's own daemon.LoadConfigBytes follow. A row holding a key this
// gateway cannot represent would otherwise be served with that key dropped,
// silently disabling whatever control it named.
func (c *SidecarConfiguration) Scan(value any) error {
	var data []byte
	switch v := value.(type) {
	case nil:
		*c = SidecarConfiguration{}
		return nil
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("failed to scan sidecar configuration, got=%T", value)
	}

	var cfg SidecarConfiguration
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("failed to scan sidecar configuration: %w", err)
	}
	*c = cfg
	return nil
}

type Sidecar struct {
	ID            string               `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID         string               `gorm:"column:org_id"`
	Name          string               `gorm:"column:name"`
	KeyHash       string               `gorm:"column:key_hash"`
	Configuration SidecarConfiguration `gorm:"column:configuration"`
	CreatedBy     string               `gorm:"column:created_by"`
	CreatedAt     time.Time            `gorm:"column:created_at"`
}

const sidecarColumns = `
	s.id, s.org_id, s.name, s.created_by, s.created_at, s.configuration`

func CreateSidecar(db *gorm.DB, s *Sidecar) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.CreatedAt = time.Now().UTC()
	err := db.Table("private.sidecars").Create(map[string]any{
		"id":            s.ID,
		"org_id":        s.OrgID,
		"name":          s.Name,
		"key_hash":      s.KeyHash,
		"configuration": s.Configuration,
		"created_by":    s.CreatedBy,
		"created_at":    s.CreatedAt,
	}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func ListSidecars(db *gorm.DB, orgID string) ([]Sidecar, error) {
	var items []Sidecar
	err := db.Raw(`
	SELECT`+sidecarColumns+`
	FROM private.sidecars s
	WHERE s.org_id = ?
	ORDER BY s.name`, orgID).
		Find(&items).
		Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

func GetSidecarByNameOrID(db *gorm.DB, orgID, nameOrID string) (*Sidecar, error) {
	identifierClause := "s.name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "s.id = ?"
	}

	var item Sidecar
	err := db.Raw(`
	SELECT`+sidecarColumns+`
	FROM private.sidecars s
	WHERE s.org_id = ? AND `+identifierClause, orgID, nameOrID).
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

// GetSidecarByKeyHash resolves the token holder. It is not org scoped: the
// token identifies the organization.
func GetSidecarByKeyHash(db *gorm.DB, keyHash string) (*Sidecar, error) {
	var item Sidecar
	err := db.Raw(`
	SELECT s.id, s.org_id, s.name, s.created_by, s.created_at, s.configuration
	FROM private.sidecars s
	WHERE s.key_hash = ?`, keyHash).
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

// UpdateSidecarConfiguration replaces the stored config document and returns
// the updated row. Returns ErrNotFound when no row matched.
func UpdateSidecarConfiguration(db *gorm.DB, orgID, nameOrID string, configuration SidecarConfiguration) (*Sidecar, error) {
	identifierClause := "name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "id = ?"
	}

	var item Sidecar
	err := db.Raw(`
	UPDATE private.sidecars
	SET configuration = ?
	WHERE org_id = ? AND `+identifierClause+`
	RETURNING id, org_id, name, created_by, created_at, configuration`,
		configuration, orgID, nameOrID).
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

// AdoptSidecarConfiguration stores the document a sidecar carried locally,
// but only while the row holds no listeners: the guard runs in the UPDATE
// itself, so a configuration authored concurrently in the control plane is
// never overwritten by a restarting sidecar. ErrAlreadyExists reports the
// guard firing; the row is known to exist because the caller authenticated
// its token.
func AdoptSidecarConfiguration(db *gorm.DB, orgID, id string, configuration SidecarConfiguration) (*Sidecar, error) {
	var item Sidecar
	err := db.Raw(`
	UPDATE private.sidecars
	SET configuration = ?
	WHERE org_id = ? AND id = ?
	  AND (jsonb_typeof(configuration->'listeners') IS DISTINCT FROM 'array'
	       OR jsonb_array_length(configuration->'listeners') = 0)
	RETURNING id, org_id, name, created_by, created_at, configuration`,
		configuration, orgID, id).
		Scan(&item).
		Error
	if err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, ErrAlreadyExists
	}
	return &item, nil
}

// DeleteSidecarByNameOrID hard deletes the row and returns its id, so the
// caller can evict any process-local runtime state.
func DeleteSidecarByNameOrID(db *gorm.DB, orgID, nameOrID string) (string, error) {
	identifierClause := "name = ?"
	if _, err := uuid.Parse(nameOrID); err == nil {
		identifierClause = "id = ?"
	}

	var deletedID string
	err := db.Raw(`
	DELETE FROM private.sidecars
	WHERE org_id = ? AND `+identifierClause+`
	RETURNING id`, orgID, nameOrID).
		Scan(&deletedID).
		Error
	if err != nil {
		return "", err
	}
	if deletedID == "" {
		return "", ErrNotFound
	}
	return deletedID, nil
}
