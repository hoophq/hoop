package models

import (
	"fmt"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// ManagedBySidecar marks the resource and role rows the control plane writes
// from a sidecar's configuration (ADR-0022 prototype).
const ManagedBySidecar = "sidecar"

// ErrSidecarTargetTaken is returned when a resource or connection with the
// projected name already exists and is not managed by a sidecar. Such a row
// is never adopted.
type ErrSidecarTargetTaken struct {
	Kind string
	Name string
}

func (e ErrSidecarTargetTaken) Error() string {
	return fmt.Sprintf("a %s named %q already exists and is not managed by a sidecar", e.Kind, e.Name)
}

// SidecarListenerRole is the role a listener is stored as.
type SidecarListenerRole struct {
	ListenerName   string `gorm:"column:listener_name"`
	ConnectionID   string `gorm:"column:connection_id"`
	ConnectionName string `gorm:"column:connection_name"`
	ResourceName   string `gorm:"column:resource_name"`
}

// UpsertSidecarResource writes the resource a sidecar is stored as and links
// the sidecar row to it.
func UpsertSidecarResource(db *gorm.DB, orgID, sidecarID, name string) error {
	var id string
	err := db.Raw(`
	INSERT INTO private.resources (org_id, name, type, subtype, agent_id, managed_by)
	VALUES (?, ?, 'custom', 'sidecar', NULL, ?)
	ON CONFLICT (org_id, name) DO UPDATE SET updated_at = NOW()
	WHERE resources.managed_by = ?
	RETURNING id`, orgID, name, ManagedBySidecar, ManagedBySidecar).
		Scan(&id).Error
	if err != nil {
		return err
	}
	if id == "" {
		return ErrSidecarTargetTaken{Kind: "resource", Name: name}
	}
	return db.Exec(`
	UPDATE private.sidecars SET resource_name = ?
	WHERE org_id = ? AND id = ?`, name, orgID, sidecarID).Error
}

// UpsertSidecarListenerRole writes the role a listener is stored as and maps
// the listener to it. It returns the role's connection id.
func UpsertSidecarListenerRole(db *gorm.DB, orgID, sidecarID, listenerName, resourceName, roleName, connType, subtype string) (string, error) {
	var id string
	err := db.Raw(`
	INSERT INTO private.connections
		(org_id, resource_name, name, type, subtype, agent_id, managed_by, command,
		 access_mode_runbooks, access_mode_exec, access_mode_connect, access_schema)
	VALUES (?, ?, ?, ?, ?, NULL, ?, ?, 'disabled', 'disabled', 'disabled', 'disabled')
	ON CONFLICT (org_id, name) DO UPDATE
		SET type = EXCLUDED.type, subtype = EXCLUDED.subtype, updated_at = NOW()
	WHERE connections.managed_by = ? AND connections.resource_name = EXCLUDED.resource_name
	RETURNING id`,
		orgID, resourceName, roleName, connType, subtype, ManagedBySidecar, pq.StringArray{}, ManagedBySidecar).
		Scan(&id).Error
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", ErrSidecarTargetTaken{Kind: "connection", Name: roleName}
	}
	err = db.Exec(`
	INSERT INTO private.sidecar_listener_roles (org_id, sidecar_id, listener_name, connection_id)
	VALUES (?, ?, ?, ?)
	ON CONFLICT (sidecar_id, listener_name) DO UPDATE SET connection_id = EXCLUDED.connection_id`,
		orgID, sidecarID, listenerName, id).Error
	return id, err
}

// DeleteSidecarListenerRolesExcept removes the roles of the listeners a
// configuration no longer declares. The mapping rows go by cascade.
func DeleteSidecarListenerRolesExcept(db *gorm.DB, orgID, sidecarID string, keep []string) error {
	// Never nil: ANY over a NULL array matches nothing, and NOT of that
	// would delete nothing instead of everything.
	if keep == nil {
		keep = []string{}
	}
	return db.Exec(`
	DELETE FROM private.connections c
	USING private.sidecar_listener_roles m
	WHERE m.connection_id = c.id
	  AND m.org_id = ? AND m.sidecar_id = ?
	  AND NOT (m.listener_name = ANY (?))
	  AND c.managed_by = ?`,
		orgID, sidecarID, pq.StringArray(keep), ManagedBySidecar).Error
}

// DeleteSidecarStorage removes the roles and the resource a sidecar is stored
// as. Call it before the sidecar row is deleted, in the same transaction.
func DeleteSidecarStorage(db *gorm.DB, orgID, sidecarID string) error {
	var resourceName *string
	err := db.Raw(`
	SELECT resource_name FROM private.sidecars WHERE org_id = ? AND id = ?`,
		orgID, sidecarID).Scan(&resourceName).Error
	if err != nil {
		return err
	}
	if err := DeleteSidecarListenerRolesExcept(db, orgID, sidecarID, nil); err != nil {
		return err
	}
	if resourceName == nil {
		return nil
	}
	if err := db.Exec(`
	UPDATE private.sidecars SET resource_name = NULL WHERE org_id = ? AND id = ?`,
		orgID, sidecarID).Error; err != nil {
		return err
	}
	// A resource still holding a role nobody projected is left in place: it
	// is not ours to delete.
	return db.Exec(`
	DELETE FROM private.resources r
	WHERE r.org_id = ? AND r.name = ? AND r.managed_by = ?
	  AND NOT EXISTS (
		SELECT 1 FROM private.connections c
		WHERE c.org_id = r.org_id AND c.resource_name = r.name)`,
		orgID, *resourceName, ManagedBySidecar).Error
}

// GetSidecarListenerRole returns the role a listener is stored as. It returns
// gorm.ErrRecordNotFound when the listener has none.
func GetSidecarListenerRole(db *gorm.DB, orgID, sidecarID, listenerName string) (*SidecarListenerRole, error) {
	var item SidecarListenerRole
	err := db.Raw(`
	SELECT m.listener_name, m.connection_id, c.name AS connection_name, c.resource_name
	FROM private.sidecar_listener_roles m
	JOIN private.connections c ON c.id = m.connection_id
	WHERE m.org_id = ? AND m.sidecar_id = ? AND m.listener_name = ?`,
		orgID, sidecarID, listenerName).Scan(&item).Error
	if err != nil {
		return nil, err
	}
	if item.ConnectionID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	return &item, nil
}

// ListSidecarListenerRoles returns every listener role of a sidecar.
func ListSidecarListenerRoles(db *gorm.DB, orgID, sidecarID string) ([]SidecarListenerRole, error) {
	var items []SidecarListenerRole
	err := db.Raw(`
	SELECT m.listener_name, m.connection_id, c.name AS connection_name, c.resource_name
	FROM private.sidecar_listener_roles m
	JOIN private.connections c ON c.id = m.connection_id
	WHERE m.org_id = ? AND m.sidecar_id = ?
	ORDER BY m.listener_name`, orgID, sidecarID).Scan(&items).Error
	return items, err
}
