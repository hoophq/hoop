package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const tableResources = "private.resources"

type Resources struct {
	ID        string    `gorm:"column:id"`
	OrgID     string    `gorm:"column:org_id"`
	Name      string    `gorm:"column:name"`
	Type      string    `gorm:"column:type"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`

	// read-only fields from related tables
	Envs map[string]string `gorm:"column:envs;serializer:json;->"`
}

func GetResourceByName(db *gorm.DB, orgID, name string, isAdminOrInternal bool) (*Resources, error) {
	var resource Resources
	err := db.Raw(`
	SELECT
		r.*,
		COALESCE((SELECT envs FROM private.env_vars WHERE (? AND id = r.id)), '{}') AS envs
	FROM private.resources r
	WHERE org_id = ? AND name = ?
	LIMIT 1
	`, isAdminOrInternal, orgID, name).First(&resource).Error
	if err != nil {
		return nil, err
	}

	return &resource, nil
}

func ListResources(db *gorm.DB, orgID string, isAdminOrInternal bool) ([]Resources, error) {
	var resources []Resources
	err := db.Raw(`
	SELECT
		r.*,
		COALESCE((SELECT envs FROM private.env_vars WHERE (? AND id = r.id)), '{}') AS envs
	FROM private.resources r
	WHERE org_id = ?
	ORDER BY created_at DESC
	`, isAdminOrInternal, orgID).Find(&resources).Error

	return resources, err
}

func UpsertResource(db *gorm.DB, resource *Resources, updateDependentTables bool) error {
	// try to find existing resource
	existing, err := GetResourceByName(db, resource.OrgID, resource.Name, true)
	switch err {
	case nil:
		resource.ID = existing.ID
		resource.UpdatedAt = time.Now().UTC()
	case gorm.ErrRecordNotFound:
		if resource.ID == "" {
			resource.ID = uuid.NewString()
		}
	default:
		return err
	}

	if existing != nil {
		err = db.Table(tableResources).Updates(&resource).Error
	} else {
		err = db.Table(tableResources).Create(&resource).Error
	}
	if err != nil {
		return err
	}

	if updateDependentTables {
		err = UpsertEnvVar(db, &EnvVar{
			ID:        resource.ID,
			OrgID:     resource.OrgID,
			Envs:      resource.Envs,
			UpdatedAt: time.Now().UTC(),
		})
	}

	return err
}

func GetResourceConnections(db *gorm.DB, orgID, resourceName string) ([]Connection, error) {
	var connections []Connection
	err := db.Table(tableConnections).
		Where("org_id = ? AND resource_name = ?", orgID, resourceName).
		Find(&connections).Error
	return connections, err
}

func DeleteResource(db *gorm.DB, orgID, name string) error {
	return db.Where("org_id = ? AND name = ?", orgID, name).Delete(&Resources{}).Error
}
