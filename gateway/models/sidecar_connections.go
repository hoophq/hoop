package models

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MaxSidecarMirrorNameLength is the width of connections.name and
// resources.name. A mirror's resource carries the mirror's name.
const MaxSidecarMirrorNameLength = 128

// sidecarMirrorSuffixLength is "-" plus eight hex digits.
const sidecarMirrorSuffixLength = 9

// SidecarMirrorFallbackName is the name a mirror takes when its preferred
// name is refused by the name rule or already in use. It is the preferred
// name reduced to the characters the rule allows, cut to fit, plus a suffix
// from the sidecar ID and the listener name. The suffix is stable, so the
// same listener always falls back to the same name, and two listeners never
// share one. A name that already ends in the suffix is returned as is, so
// the fallback of a fallback name is that name.
func SidecarMirrorFallbackName(preferred, sidecarID, listener string) string {
	sum := sha256.Sum256([]byte(sidecarID + "/" + listener))
	suffix := "-" + hex.EncodeToString(sum[:])[:sidecarMirrorSuffixLength-1]
	if strings.HasSuffix(preferred, suffix) && len(preferred) <= MaxSidecarMirrorNameLength {
		return preferred
	}

	var b strings.Builder
	sep := false
	for _, r := range preferred {
		ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		switch {
		case ok:
			if sep && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			sep = false
		default:
			sep = true
		}
	}
	base := b.String()
	if max := MaxSidecarMirrorNameLength - sidecarMirrorSuffixLength; len(base) > max {
		base = strings.TrimRight(base[:max], "-")
	}
	if base == "" {
		base = "sidecar"
	}
	return base + suffix
}

// ErrSidecarConnectionNameTaken is a mirror the sync could not create: both
// its name and its fallback name are in use by another connection or
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
			byListener[row.Listener] = row.Name
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
		if name, ok := byListener[m.SidecarListener.String]; ok {
			if err := updateSidecarConnection(tx, name, m); err != nil {
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

// updateSidecarConnection rewrites the projected columns of a mirror that
// already exists. It never renames it: other tables key a connection by its
// name (attributes, access request rules, event subscriptions, sessions), and
// a mirror's name is fixed when it is created. The sidecar name cannot
// change, and a renamed listener is a new mirror.
func updateSidecarConnection(tx *gorm.DB, name string, m Connection) error {
	// Its own resource, written before the connection points back at it: an
	// admin may have re-pointed the mirror and deleted the resource.
	err := tx.Exec(`INSERT INTO private.resources (org_id, name, type, subtype) VALUES (?, ?, ?, ?)
		ON CONFLICT (org_id, name) DO UPDATE SET type = EXCLUDED.type, subtype = EXCLUDED.subtype, updated_at = NOW()`,
		m.OrgID, name, m.Type, m.SubType).Error
	if err != nil {
		return fmt.Errorf("failed writing resource %q, reason=%v", name, err)
	}
	err = tx.Exec(`UPDATE private.connections
		SET resource_name = name, type = ?, subtype = ?, managed_by = ?, agent_id = NULL,
		    access_mode_runbooks = ?, access_mode_exec = ?, access_mode_connect = ?, access_schema = ?,
		    updated_at = NOW()
		WHERE org_id = ? AND name = ?`,
		m.Type, m.SubType, m.ManagedBy,
		m.AccessModeRunbooks, m.AccessModeExec, m.AccessModeConnect, m.AccessSchema, m.OrgID, name).Error
	if err != nil {
		return fmt.Errorf("failed updating connection %q, reason=%v", name, err)
	}
	return nil
}

// insertSidecarConnection creates a mirror under m.Name, or under its
// fallback name when m.Name is in use. A name in use never refuses the
// sidecar write: the sidecar config is the source of truth, and it may come
// from a file the admin cannot rename from here.
//
// Every write is ON CONFLICT DO NOTHING, so a name another writer takes in
// between moves to the next candidate instead of aborting the transaction.
func insertSidecarConnection(tx *gorm.DB, m Connection) error {
	candidates := []string{m.Name}
	if fallback := SidecarMirrorFallbackName(m.Name, m.SidecarID.String, m.SidecarListener.String); fallback != m.Name {
		candidates = append(candidates, fallback)
	}
	for _, name := range candidates {
		created, err := createSidecarConnection(tx, name, m)
		if err != nil {
			return err
		}
		if created {
			return nil
		}
	}
	return ErrSidecarConnectionNameTaken{Listener: m.SidecarListener.String, Name: candidates[len(candidates)-1]}
}

// createSidecarConnection reports false, with nothing written, when name is
// in use.
//
// A resource is reused only when no connection uses it and it names no
// agent: a connection reads its agent from its resource when it has none
// (getConnectionByNameOrID), so a mirror must never inherit one.
func createSidecarConnection(tx *gorm.DB, name string, m Connection) (bool, error) {
	var inUse bool
	err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM private.connections WHERE org_id = ? AND (name = ? OR resource_name = ?))`,
		m.OrgID, name, name).Scan(&inUse).Error
	if err != nil {
		return false, fmt.Errorf("failed checking connection %q, reason=%v", name, err)
	}
	if inUse {
		return false, nil
	}
	res := tx.Exec(`INSERT INTO private.resources AS r (org_id, name, type, subtype) VALUES (?, ?, ?, ?)
		ON CONFLICT (org_id, name) DO UPDATE SET type = EXCLUDED.type, subtype = EXCLUDED.subtype, updated_at = NOW()
		WHERE r.agent_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM private.connections c WHERE c.org_id = r.org_id AND c.resource_name = r.name)`,
		m.OrgID, name, m.Type, m.SubType)
	if res.Error != nil {
		return false, fmt.Errorf("failed writing resource %q, reason=%v", name, res.Error)
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	res = tx.Exec(`INSERT INTO private.connections
		(id, org_id, name, resource_name, type, subtype, status, managed_by, sidecar_id, sidecar_listener,
		 access_mode_runbooks, access_mode_exec, access_mode_connect, access_schema)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (org_id, name) DO NOTHING`,
		uuid.NewString(), m.OrgID, name, name, m.Type, m.SubType, ConnectionStatusOffline, m.ManagedBy,
		m.SidecarID, m.SidecarListener,
		m.AccessModeRunbooks, m.AccessModeExec, m.AccessModeConnect, m.AccessSchema)
	if res.Error != nil {
		return false, fmt.Errorf("failed creating connection %q, reason=%v", name, res.Error)
	}
	if res.RowsAffected == 0 {
		// Taken between the check and the insert. The resource written
		// above has no connection, so it goes too.
		return false, deleteOrphanResource(tx, m.OrgID, name)
	}
	return true, nil
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
