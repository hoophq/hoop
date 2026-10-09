package services

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/inspect"
	"gorm.io/gorm"
)

// ErrSidecarListenerInvalid is a listener no connection can mirror: its
// protocol has no connection type, or its name cannot address it. The admin's
// to fix, so it reads 422.
type ErrSidecarListenerInvalid struct{ Err error }

func (e ErrSidecarListenerInvalid) Error() string { return e.Err.Error() }
func (e ErrSidecarListenerInvalid) Unwrap() error { return e.Err }

// SyncSidecarListenerConnectionsTx writes the mirror of every listener of sc,
// in the transaction that stored its configuration. Every write of the
// configuration must call it: a listener without a mirror is one no rule can
// bind to.
//
// A mirror stays until the next write of the sidecar, or until the sidecar is
// deleted.
func SyncSidecarListenerConnectionsTx(tx *gorm.DB, sc *models.Sidecar) error {
	mirrors, err := ProjectListeners(sc.OrgID, sc)
	if err != nil {
		return ErrSidecarListenerInvalid{err}
	}
	return models.SyncSidecarConnectionsTx(tx, sc.OrgID, sc.ID, mirrors)
}

// ReconcileSidecarListenerConnections writes the mirrors of every sidecar of
// orgID, one transaction per sidecar, for the rows that fell behind the write
// path. A sidecar it cannot mirror is skipped and returned, never fatal: its
// next write answers the same error to the admin.
func ReconcileSidecarListenerConnections(db *gorm.DB, orgID string) []error {
	sidecars, err := models.ListSidecars(db, orgID)
	if err != nil {
		return []error{fmt.Errorf("failed listing sidecars, reason=%v", err)}
	}
	var failed []error
	for _, listed := range sidecars {
		err := db.Transaction(func(tx *gorm.DB) error {
			// Read again under the lock the config writers take: a write that
			// lands after the listing must not be undone by its older copy.
			sc, err := models.GetSidecarByNameOrIDForUpdate(tx, orgID, listed.ID)
			if err != nil {
				return err
			}
			return SyncSidecarListenerConnectionsTx(tx, sc)
		})
		switch {
		case errors.Is(err, models.ErrNotFound):
			// Deleted after the listing: the cascade took its mirrors.
		case err != nil:
			failed = append(failed, fmt.Errorf("sidecar %q: %w", listed.Name, err))
		}
	}
	return failed
}

// ReconcileAllSidecarListenerConnections runs the reconcile for every org.
// Called at startup: it covers an org whose mirrors fell behind while this
// gateway was not running, such as a redeploy after a rollback. It logs and
// never stops the startup.
func ReconcileAllSidecarListenerConnections(db *gorm.DB) {
	orgs, err := models.ListAllOrganizations()
	if err != nil {
		log.Warnf("sidecar mirrors: failed listing orgs, reason=%v", err)
		return
	}
	for _, org := range orgs {
		for _, err := range ReconcileSidecarListenerConnections(db, org.ID) {
			log.With("org", org.ID).Warnf("sidecar mirrors: %v", err)
		}
	}
}

// listenerConnectionKind is the connection type and subtype a listener
// protocol projects to. A protocol absent here is an error, never a default:
// the type decides which proxy, which UI and which rules apply.
var listenerConnectionKind = map[inspect.Protocol]struct{ typ, subtype string }{
	inspect.Postgres:   {"database", string(proto.ConnectionTypePostgres)},
	inspect.MySQL:      {"database", string(proto.ConnectionTypeMySQL)},
	inspect.MSSQL:      {"database", string(proto.ConnectionTypeMSSQL)},
	inspect.MongoDB:    {"database", string(proto.ConnectionTypeMongoDB)},
	inspect.Oracle:     {"database", string(proto.ConnectionTypeOracleDB)},
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
// A listener with no name, or with the name of an earlier listener, is
// refused. Its mirror is how every feature reads it, so it must have one: a
// listener left out would be left out of those features with no error. Writes
// already refuse both for every org (ValidateListenerNames); here they guard
// rows stored before that check. Every message names the listener by position.
//
// The name length is not checked: the sidecar sets no limit, and the listener
// columns are TEXT. Only the Postgres index limit (about 2.6 KB) fails a write.
//
// Every access mode is disabled: the gateway has no route to a sidecar, so a
// client connects to the listener itself, and exec, runbooks and the schema
// browser have nobody to run them.
func ProjectListeners(orgID string, sc *models.Sidecar) ([]models.Connection, error) {
	listeners := sc.Configuration.Listeners
	out := make([]models.Connection, 0, len(listeners))
	seen := make(map[string]int, len(listeners))
	for i, l := range listeners {
		kind, ok := listenerConnectionKind[inspect.Protocol(l.Protocol)]
		if !ok {
			return nil, fmt.Errorf("listener %q: no connection type for protocol %q", l.Name, l.Protocol)
		}
		if l.Name == "" {
			return nil, fmt.Errorf("listeners[%d]: no name; name the listener to manage it as a resource", i)
		}
		if first, ok := seen[l.Name]; ok {
			return nil, fmt.Errorf("listeners[%d]: the name %q repeats listeners[%d]; give each listener its own name to manage it as a resource",
				i, l.Name, first)
		}
		seen[l.Name] = i
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
			AccessModeConnect:  "disabled",
			AccessModeExec:     "disabled",
			AccessModeRunbooks: "disabled",
			AccessSchema:       "disabled",
		})
	}
	return out, nil
}
