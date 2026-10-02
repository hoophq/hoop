package models

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrSidecarConnectionNameTaken is a mirror the sync could not create: a
// connection that is not this listener's already has the name.
type ErrSidecarConnectionNameTaken struct{ Listener, Name string }

func (e ErrSidecarConnectionNameTaken) Error() string {
	return fmt.Sprintf("listener %q: a connection named %q already exists; rename the listener", e.Listener, e.Name)
}

// SyncSidecarConnectionsTx makes the connections of one sidecar match
// mirrors: one per listener, keyed by (org_id, sidecar_id, sidecar_listener).
//
// A listener that is gone takes its connection with it, and the cascade takes
// what was bound to the connection. A listener that stays keeps its row and
// its ID, so what an admin attached to the connection survives; only the
// projected columns are written. Runs inside the caller's transaction, with
// the config write it mirrors.
func SyncSidecarConnectionsTx(tx *gorm.DB, orgID, sidecarID string, mirrors []Connection) error {
	var stored []struct {
		ID           string `gorm:"column:id"`
		ResourceName string `gorm:"column:resource_name"`
		Listener     string `gorm:"column:sidecar_listener"`
	}
	err := tx.Raw(`SELECT id, resource_name, sidecar_listener FROM private.connections
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
		if err := deleteSidecarConnection(tx, orgID, row.ID, row.ResourceName); err != nil {
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

func updateSidecarConnection(tx *gorm.DB, id string, m Connection) error {
	err := tx.Exec(`UPDATE private.resources SET type = ?, subtype = ?, updated_at = NOW()
		WHERE org_id = ? AND name = ?`, m.Type, m.SubType, m.OrgID, m.Name).Error
	if err != nil {
		return fmt.Errorf("failed updating resource %q, reason=%v", m.Name, err)
	}
	err = tx.Exec(`UPDATE private.connections
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
	var taken bool
	err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM private.connections WHERE org_id = ? AND name = ?)`,
		m.OrgID, m.Name).Scan(&taken).Error
	if err != nil {
		return fmt.Errorf("failed checking connection %q, reason=%v", m.Name, err)
	}
	if taken {
		return ErrSidecarConnectionNameTaken{Listener: m.SidecarListener.String, Name: m.Name}
	}
	// The mirror's own resource, as UpsertConnection defaults it. One left
	// behind by a connection that is gone is reused rather than refused.
	err = tx.Exec(`INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, ?, ?, ?)
		ON CONFLICT (org_id, name) DO UPDATE SET type = EXCLUDED.type, subtype = EXCLUDED.subtype, updated_at = NOW()`,
		m.OrgID, m.Name, m.Type, m.SubType).Error
	if err != nil {
		return fmt.Errorf("failed creating resource %q, reason=%v", m.Name, err)
	}
	err = tx.Exec(`INSERT INTO private.connections
		(id, org_id, name, resource_name, type, subtype, status, managed_by, sidecar_id, sidecar_listener,
		 access_mode_runbooks, access_mode_exec, access_mode_connect, access_schema)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), m.OrgID, m.Name, m.Name, m.Type, m.SubType, ConnectionStatusOffline, m.ManagedBy,
		m.SidecarID, m.SidecarListener,
		m.AccessModeRunbooks, m.AccessModeExec, m.AccessModeConnect, m.AccessSchema).Error
	if err != nil {
		return fmt.Errorf("failed creating connection %q, reason=%v", m.Name, err)
	}
	return nil
}

// deleteSidecarConnection removes a mirror and the resource it alone used.
func deleteSidecarConnection(tx *gorm.DB, orgID, id, resourceName string) error {
	if err := tx.Exec(`DELETE FROM private.connections WHERE id = ?`, id).Error; err != nil {
		return fmt.Errorf("failed deleting sidecar connection, reason=%v", err)
	}
	err := tx.Exec(`DELETE FROM private.resources r WHERE r.org_id = ? AND r.name = ?
		AND NOT EXISTS (SELECT 1 FROM private.connections c WHERE c.org_id = r.org_id AND c.resource_name = r.name)`,
		orgID, resourceName).Error
	if err != nil {
		return fmt.Errorf("failed deleting resource %q, reason=%v", resourceName, err)
	}
	return nil
}
