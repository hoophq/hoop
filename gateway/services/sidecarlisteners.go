package services

import (
	"database/sql"
	"fmt"

	"github.com/hoophq/hoop/common/proto"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/inspect"
	"gorm.io/gorm"
)

// ErrSidecarListenerInvalid is a listener no connection can mirror: its
// protocol has no connection type. The admin's to fix, so it reads 422.
type ErrSidecarListenerInvalid struct{ Err error }

func (e ErrSidecarListenerInvalid) Error() string { return e.Err.Error() }
func (e ErrSidecarListenerInvalid) Unwrap() error { return e.Err }

// SyncSidecarListenerConnectionsTx writes the mirror of every listener of sc,
// in the transaction that stored its configuration. Every write of the
// configuration must call it: a listener without a mirror is one no rule can
// bind to.
//
// It does nothing while the org has beta.sidecar_listeners off. Nothing reads
// a mirror yet, and an org that has not opted in must keep its connection
// list, its sidecar writes and its imports exactly as before. A mirror
// written while the flag was on stays until a write with the flag on, or
// until the sidecar is deleted.
func SyncSidecarListenerConnectionsTx(tx *gorm.DB, sc *models.Sidecar) error {
	if !SidecarListenersEnabled(sc.OrgID) {
		return nil
	}
	mirrors, err := ProjectListeners(sc.OrgID, sc)
	if err != nil {
		return ErrSidecarListenerInvalid{err}
	}
	return models.SyncSidecarConnectionsTx(tx, sc.OrgID, sc.ID, mirrors)
}

// listenerConnectionKind is the connection type and subtype a listener
// protocol projects to. A protocol absent here is an error, never a default:
// the type decides which proxy, which UI and which rules apply.
var listenerConnectionKind = map[inspect.Protocol]struct{ typ, subtype string }{
	inspect.Postgres:   {"database", string(proto.ConnectionTypePostgres)},
	inspect.MySQL:      {"database", string(proto.ConnectionTypeMySQL)},
	inspect.MSSQL:      {"database", string(proto.ConnectionTypeMSSQL)},
	inspect.MongoDB:    {"database", string(proto.ConnectionTypeMongoDB)},
	inspect.SSH:        {"application", string(proto.ConnectionTypeSSH)},
	inspect.HTTP:       {"httpproxy", string(proto.ConnectionTypeHttpProxy)},
	inspect.ClickHouse: {"custom", string(inspect.ClickHouse)},
	inspect.GRPC:       {"custom", string(inspect.GRPC)},
	inspect.Spanner:    {"custom", string(inspect.Spanner)},
}

// ProjectListeners renders the mirror connection of every listener of sc, in
// the order of the configuration. It is pure: no database, no side effects.
//
// The ID is left empty; the writer resolves it by (org_id, sidecar_id,
// sidecar_listener). Status and ResourceName are left empty too: both are
// decided where the row is written.
//
// The name is <sidecar>-<listener>. A listener name the connection name rule
// refuses (a space, a slash, too long) takes the fallback name instead: the
// daemon accepts those names, so refusing them would refuse a sidecar config
// that works today.
//
// A listener with no name, or with the name of an earlier listener, has no
// mirror. Rules bind to a listener by its name, so neither one can be
// addressed, and the daemon accepts both.
//
// No agent runs a mirror, so only connect is enabled: exec, runbooks and the
// schema browser have nobody to run them.
func ProjectListeners(orgID string, sc *models.Sidecar) ([]models.Connection, error) {
	listeners := sc.Configuration.Listeners
	out := make([]models.Connection, 0, len(listeners))
	seen := make(map[string]bool, len(listeners))
	for _, l := range listeners {
		kind, ok := listenerConnectionKind[inspect.Protocol(l.Protocol)]
		if !ok {
			return nil, fmt.Errorf("listener %q: no connection type for protocol %q", l.Name, l.Protocol)
		}
		if l.Name == "" || seen[l.Name] {
			continue
		}
		seen[l.Name] = true
		name := sc.Name + "-" + l.Name
		if apivalidation.ValidateResourceName(name) != nil || len(name) > models.MaxSidecarMirrorNameLength {
			name = models.SidecarMirrorFallbackName(name, sc.ID, l.Name)
		}
		out = append(out, models.Connection{
			OrgID:              orgID,
			Name:               name,
			Type:               kind.typ,
			SubType:            sql.NullString{String: kind.subtype, Valid: true},
			ManagedBy:          sql.NullString{String: models.ConnectionManagedBySidecar, Valid: true},
			SidecarID:          sql.NullString{String: sc.ID, Valid: true},
			SidecarListener:    sql.NullString{String: l.Name, Valid: true},
			AccessModeConnect:  "enabled",
			AccessModeExec:     "disabled",
			AccessModeRunbooks: "disabled",
			AccessSchema:       "disabled",
		})
	}
	return out, nil
}
