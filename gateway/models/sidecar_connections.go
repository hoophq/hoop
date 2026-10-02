package models

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrSidecarConnectionNameTaken is a mirror the sync could not create: a
// connection that is not this listener's has the name, or uses it as its
// resource.
type ErrSidecarConnectionNameTaken struct{ Listener, Name string }

func (e ErrSidecarConnectionNameTaken) Error() string {
	return fmt.Sprintf("listener %q: the name %q is in use by another connection; rename the listener", e.Listener, e.Name)
}

// ErrSidecarConnectionInUse is a mirror the sync could not delete: an event
// subscription still names it.
type ErrSidecarConnectionInUse struct{ Listener, Name string }

func (e ErrSidecarConnectionInUse) Error() string {
	return fmt.Sprintf("listener %q: connection %q is used by an event subscription; remove the subscription first", e.Listener, e.Name)
}

// SyncSidecarConnectionsTx makes the connections of one sidecar match
// mirrors: one per listener, keyed by (org_id, sidecar_id, sidecar_listener).
//
// A listener that is gone takes its connection with it, and the cascade takes
// what was bound to the connection. A listener that stays keeps its row and
// its ID, so what an admin attached to the connection survives; only the
// projected columns are written. Runs inside the caller's transaction, with
// the config write it mirrors.
//
// The sidecar row is locked first, before its connections: the config
// writers lock it before they get here, and a second path that locked the
// connections first would deadlock with them.
func SyncSidecarConnectionsTx(tx *gorm.DB, orgID, sidecarID string, mirrors []Connection) error {
	var locked string
	err := tx.Raw(`SELECT id FROM private.sidecars WHERE org_id = ? AND id = ? FOR UPDATE`, orgID, sidecarID).
		Scan(&locked).Error
	if err != nil {
		return fmt.Errorf("failed locking sidecar, reason=%v", err)
	}
	if locked == "" {
		return ErrNotFound
	}
	var stored []struct {
		ID           string `gorm:"column:id"`
		Name         string `gorm:"column:name"`
		ResourceName string `gorm:"column:resource_name"`
		Listener     string `gorm:"column:sidecar_listener"`
	}
	err = tx.Raw(`SELECT id, name, resource_name, sidecar_listener FROM private.connections
		WHERE org_id = ? AND sidecar_id = ? FOR UPDATE`, orgID, sidecarID).Scan(&stored).Error
	if err != nil {
		return fmt.Errorf("failed loading sidecar connections, reason=%v", err)
	}
	keep := make(map[string]bool, len(mirrors))
	for _, m := range mirrors {
		keep[m.SidecarListener.String] = true
	}
	byListener := make(map[string]string, len(stored))
	for _, row := range stored {
		if keep[row.Listener] {
			byListener[row.Listener] = row.ID
			continue
		}
		if err := tx.Exec(`DELETE FROM private.connections WHERE id = ?`, row.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrForeignKeyViolated) {
				return ErrSidecarConnectionInUse{Listener: row.Listener, Name: row.Name}
			}
			return fmt.Errorf("failed deleting connection %q, reason=%v", row.Name, err)
		}
		if err := deleteOrphanResource(tx, orgID, row.ResourceName); err != nil {
			return err
		}
	}
	for _, m := range mirrors {
		if id, ok := byListener[m.SidecarListener.String]; ok {
			if err := updateSidecarConnection(tx, id, m); err != nil {
				return err
			}
			continue
		}
		if err := insertSidecarConnection(tx, m); err != nil {
			return err
		}
	}
	return nil
}

// upsertSidecarResource writes the mirror's own resource, as UpsertConnection
// defaults it. One a deleted connection left behind is reused; the caller
// checks that no other connection uses it.
func upsertSidecarResource(tx *gorm.DB, m Connection) error {
	err := tx.Exec(`INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, ?, ?, ?)
		ON CONFLICT (org_id, name) DO UPDATE SET type = EXCLUDED.type, subtype = EXCLUDED.subtype, updated_at = NOW()`,
		m.OrgID, m.Name, m.Type, m.SubType).Error
	if err != nil {
		return fmt.Errorf("failed writing resource %q, reason=%v", m.Name, err)
	}
	return nil
}

func updateSidecarConnection(tx *gorm.DB, id string, m Connection) error {
	// Written before the connection points at it: an admin may have
	// re-pointed the mirror and deleted the resource it was projected with.
	if err := upsertSidecarResource(tx, m); err != nil {
		return err
	}
	err := tx.Exec(`UPDATE private.connections
		SET name = ?, resource_name = ?, type = ?, subtype = ?, managed_by = ?, agent_id = NULL,
		    access_mode_runbooks = ?, access_mode_exec = ?, access_mode_connect = ?, access_schema = ?,
		    updated_at = NOW()
		WHERE id = ?`,
		m.Name, m.Name, m.Type, m.SubType, m.ManagedBy,
		m.AccessModeRunbooks, m.AccessModeExec, m.AccessModeConnect, m.AccessSchema, id).Error
	if err != nil {
		return fmt.Errorf("failed updating connection %q, reason=%v", m.Name, err)
	}
	return nil
}

func insertSidecarConnection(tx *gorm.DB, m Connection) error {
	taken := ErrSidecarConnectionNameTaken{Listener: m.SidecarListener.String, Name: m.Name}
	var inUse bool
	err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM private.connections WHERE org_id = ? AND (name = ? OR resource_name = ?))`,
		m.OrgID, m.Name, m.Name).Scan(&inUse).Error
	if err != nil {
		return fmt.Errorf("failed checking connection %q, reason=%v", m.Name, err)
	}
	if inUse {
		return taken
	}
	if err := upsertSidecarResource(tx, m); err != nil {
		return err
	}
	err = tx.Exec(`INSERT INTO private.connections
		(id, org_id, name, resource_name, type, subtype, status, managed_by, sidecar_id, sidecar_listener,
		 access_mode_runbooks, access_mode_exec, access_mode_connect, access_schema)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), m.OrgID, m.Name, m.Name, m.Type, m.SubType, ConnectionStatusOffline, m.ManagedBy,
		m.SidecarID, m.SidecarListener,
		m.AccessModeRunbooks, m.AccessModeExec, m.AccessModeConnect, m.AccessSchema).Error
	if err != nil {
		// Another sidecar composed the same name between the check and
		// the insert; only its lock is held, so the unique key decides.
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return taken
		}
		return fmt.Errorf("failed creating connection %q, reason=%v", m.Name, err)
	}
	return nil
}

// deleteOrphanResource removes a resource no connection uses any more.
func deleteOrphanResource(tx *gorm.DB, orgID, name string) error {
	err := tx.Exec(`DELETE FROM private.resources r WHERE r.org_id = ? AND r.name = ?
		AND NOT EXISTS (SELECT 1 FROM private.connections c WHERE c.org_id = r.org_id AND c.resource_name = r.name)`,
		orgID, name).Error
	if err != nil {
		return fmt.Errorf("failed deleting resource %q, reason=%v", name, err)
	}
	return nil
}
