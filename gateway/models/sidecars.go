package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type Sidecar struct {
	ID        string    `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	OrgID     string    `gorm:"column:org_id"`
	Name      string    `gorm:"column:name"`
	KeyHash   string    `gorm:"column:key_hash"`
	CreatedBy string    `gorm:"column:created_by"`
	CreatedAt time.Time `gorm:"column:created_at"`
	// Connections is derived, not stored: the names of the connections whose
	// sidecar_id points here.
	Connections pq.StringArray `gorm:"column:connections;type:text[];->"`
}

const sidecarColumns = `
	s.id, s.org_id, s.name, s.created_by, s.created_at,
	COALESCE((
		SELECT array_agg(c.name::TEXT) FROM private.connections c
		WHERE c.sidecar_id = s.id
	), ARRAY[]::TEXT[]) AS connections`

func CreateSidecar(db *gorm.DB, s *Sidecar) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.CreatedAt = time.Now().UTC()
	err := db.Table("private.sidecars").Create(map[string]any{
		"id":         s.ID,
		"org_id":     s.OrgID,
		"name":       s.Name,
		"key_hash":   s.KeyHash,
		"created_by": s.CreatedBy,
		"created_at": s.CreatedAt,
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
	SELECT s.id, s.org_id, s.name, s.created_by, s.created_at
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
