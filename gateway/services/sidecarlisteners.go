package services

import (
	"database/sql"
	"fmt"

	"github.com/hoophq/hoop/common/proto"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/inspect"
)

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
// decided where the row is written. The name is <sidecar>-<listener> and must
// pass the connection name rule, so a listener the rule refuses is an error
// rather than a mirror nothing can address.
//
// No agent runs a mirror, so only connect is enabled: exec, runbooks and the
// schema browser have nobody to run them.
func ProjectListeners(orgID string, sc *models.Sidecar) ([]models.Connection, error) {
	listeners := sc.Configuration.Listeners
	out := make([]models.Connection, 0, len(listeners))
	for _, l := range listeners {
		kind, ok := listenerConnectionKind[inspect.Protocol(l.Protocol)]
		if !ok {
			return nil, fmt.Errorf("listener %q: no connection type for protocol %q", l.Name, l.Protocol)
		}
		name := sc.Name + "-" + l.Name
		if err := apivalidation.ValidateResourceName(name); err != nil {
			return nil, fmt.Errorf("listener %q: connection %s", l.Name, err)
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
