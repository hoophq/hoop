package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

const tableResources = "private.resources"

type Resources struct {
	ID        string         `gorm:"column:id"`
	OrgID     string         `gorm:"column:org_id"`
	Name      string         `gorm:"column:name"`
	Type      string         `gorm:"column:type"`
	SubType   sql.NullString `gorm:"column:subtype"`
	AgentID   sql.NullString `gorm:"column:agent_id"`
	CreatedAt time.Time      `gorm:"column:created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at"`

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

type ResourceFilterOption struct {
	Page     int
	PageSize int
	Search   string
	Name     string
	SubType  string
}

func setResourceOptionDefaults(opts *ResourceFilterOption) {
	if opts.SubType == "" {
		opts.SubType = "%"
	}
}

func ListResources(db *gorm.DB, orgID string, userGroups []string, isAdminOrInternal bool, opts ResourceFilterOption) ([]Resources, int64, error) {
	setResourceOptionDefaults(&opts)

	offset := 0
	if opts.Page > 1 {
		offset = (opts.Page - 1) * opts.PageSize
	}

	// Gorm has issues with optional LIMIT in raw queries, so we build the query string conditionally
	paginationQuery := "LIMIT @page_size OFFSET @offset"
	if opts.PageSize == 0 {
		paginationQuery = ""
	}

	nameQuery := "%"
	if opts.Name != "" {
		nameQuery = "%" + opts.Name + "%"
	}

	searchQuery := "%"
	if opts.Search != "" {
		searchQuery = "%" + opts.Search + "%"
	}

	var results []struct {
		Resources
		Total int64 `gorm:"column:total"`
	}

	err := db.Raw(`
	SELECT
		r.*,
		COALESCE((SELECT envs FROM private.env_vars WHERE (@is_admin_or_internal AND id = r.id)), '{}') AS envs,
		COUNT(*) OVER() AS total
	FROM private.resources r
		LEFT JOIN private.connections c ON r.org_id = c.org_id AND r.name = c.resource_name
		LEFT JOIN private.plugins ac ON ac.name = 'access_control' AND ac.org_id = @org_id
		LEFT JOIN private.plugin_connections acc ON acc.connection_id = c.id AND acc.plugin_id = ac.id
	WHERE
		r.org_id = @org_id AND
		CASE
				-- do not apply any access control if the plugin is not enabled or it is an admin user
				WHEN ac.id IS NULL OR (@is_admin_or_internal)::BOOL THEN true
				-- allow if any of the input user groups are in the access control list
				ELSE acc.config && (@user_groups)::text[]
		END AND
		r.name LIKE @name AND
		r.subtype LIKE @subtype AND
		(
			r.name LIKE @search OR
			COALESCE(r.subtype, '') LIKE @search OR
			r.type::text LIKE @search
		)
	ORDER BY created_at DESC `+paginationQuery, map[string]interface{}{
		"org_id":               orgID,
		"user_groups":          pq.StringArray(userGroups),
		"is_admin_or_internal": isAdminOrInternal,
		"search":               searchQuery,
		"name":                 nameQuery,
		"subtype":              opts.SubType,
		"page_size":            opts.PageSize,
		"offset":               offset,
	}).Find(&results).Error
	if err != nil {
		return nil, 0, err
	}

	if len(results) == 0 {
		return []Resources{}, 0, nil
	}

	total := results[0].Total
	resources := make([]Resources, len(results))
	for i, r := range results {
		resources[i] = r.Resources
	}

	return resources, total, err
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
