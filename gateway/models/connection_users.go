package models

import (
	"database/sql"
	"errors"

	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// The path that lets a user open a connection, in the order the access check
// in getConnectionByNameOrID tests them.
const (
	ConnectionAccessAll     = "all" // the access_control plugin is off
	ConnectionAccessAdmin   = "admin"
	ConnectionAccessAuditor = "auditor"
	ConnectionAccessGroups  = "groups"
)

// ConnectionUser is a user who can open a connection.
type ConnectionUser struct {
	Subject    string `gorm:"column:subject"`
	Name       string `gorm:"column:name"`
	Email      string `gorm:"column:email"`
	Status     string `gorm:"column:status"`
	AccessType string `gorm:"column:access_type"`
	// Groups of the user in the connection's access_control config.
	Groups pq.StringArray `gorm:"column:groups;type:text[]"`
	// Groups of the user linked to the connection through an attribute.
	AttributeGroups pq.StringArray `gorm:"column:attribute_groups;type:text[]"`
}

// connectionUsersQuery selects every user of the org with the path that lets
// them open the connection @conn_id, or a NULL access_type when none does.
const connectionUsersQuery = `
	WITH conn AS (
		SELECT c.name, ac.id IS NOT NULL AS access_control_on,
			COALESCE(acc.config, ARRAY[]::TEXT[]) AS config
		FROM private.connections c
		LEFT JOIN private.plugins ac ON ac.name = 'access_control' AND ac.org_id = c.org_id
		LEFT JOIN private.plugin_connections acc ON acc.connection_id = c.id AND acc.plugin_id = ac.id
		WHERE c.id = (@conn_id)::UUID
	), attr AS (
		SELECT COALESCE(array_agg(DISTINCT acga.group_name::TEXT), ARRAY[]::TEXT[]) AS groups
		FROM private.access_control_groups_attributes acga
		JOIN private.connections_attributes ca
			ON ca.org_id = acga.org_id AND ca.attribute_name = acga.attribute_name
		JOIN conn ON ca.connection_name = conn.name
		WHERE acga.org_id = @org_id
	), usr AS (
		SELECT u.subject, u.name, u.email, u.status::TEXT AS status,
			COALESCE((
				SELECT array_agg(ug.name::TEXT) FROM private.user_groups ug WHERE ug.user_id = u.id
			), ARRAY[]::TEXT[]) AS groups
		FROM private.users u
		WHERE u.org_id = @org_id
	), access AS (
		SELECT u.subject, u.name, u.email, u.status,
			CASE
				WHEN NOT conn.access_control_on THEN @all
				WHEN (@admin_group)::TEXT = ANY(u.groups) THEN @admin
				WHEN (@auditor_group)::TEXT = ANY(u.groups) THEN @auditor
				WHEN u.groups && conn.config OR u.groups && attr.groups THEN @groups
			END AS access_type,
			ARRAY(SELECT unnest(u.groups) INTERSECT SELECT unnest(conn.config) ORDER BY 1) AS groups,
			ARRAY(SELECT unnest(u.groups) INTERSECT SELECT unnest(attr.groups) ORDER BY 1) AS attribute_groups
		FROM usr u, conn, attr
	)`

// ListConnectionUsers lists the users who can open a connection, ordered by
// email. It is the inverse of the access check in getConnectionByNameOrID;
// TestListConnectionUsersMatchesConnectionRead fails when the two disagree.
// It returns gorm.ErrRecordNotFound when the connection does not exist.
func ListConnectionUsers(db *gorm.DB, orgID, nameOrID string, page, pageSize int) ([]ConnectionUser, int64, error) {
	var connID string
	err := db.Raw(`
	SELECT id::TEXT FROM private.connections
	WHERE org_id = ? AND (name = ? OR id::TEXT = ?)`, orgID, nameOrID, nameOrID).
		Row().Scan(&connID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, gorm.ErrRecordNotFound
	}
	if err != nil {
		return nil, 0, err
	}

	params := map[string]any{
		"conn_id":       connID,
		"org_id":        orgID,
		"admin_group":   types.GroupAdmin,
		"auditor_group": types.GroupAuditor,
		"all":           ConnectionAccessAll,
		"admin":         ConnectionAccessAdmin,
		"auditor":       ConnectionAccessAuditor,
		"groups":        ConnectionAccessGroups,
		"limit":         pageSize,
		"offset":        (page - 1) * pageSize,
	}
	var total int64
	err = db.Raw(connectionUsersQuery+`
	SELECT COUNT(*) FROM access WHERE access_type IS NOT NULL`, params).
		Scan(&total).Error
	if err != nil {
		return nil, 0, err
	}
	items := []ConnectionUser{}
	err = db.Raw(connectionUsersQuery+`
	SELECT * FROM access
	WHERE access_type IS NOT NULL
	ORDER BY email, subject
	LIMIT @limit OFFSET @offset`, params).
		Scan(&items).Error
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
