// snapshot.go — go look at the database and assemble a Snapshot.
//
// Three responsibilities:
//
//   1. SQL templates (clusterTmpl, databaseTmpl) — the introspection queries
//      that read role and per-database state out of the system catalogs.
//   2. Snapshot types (Snapshot, Scope, Exceptions) and their JSON wire format
//      (clusterJSON, databaseJSON, schemaJSON, exceptionsJSON).
//   3. Snapshot-taking functions: clusterSnapshot (cluster query only, used
//      by both modes) and takeSnapshot (cluster + per-database fan-out, used
//      by managed mode). Plus the small psql runner and the external-mode
//      parent-existence precondition.

package pgmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// SQL templates. The SQL is now smaller because we only emit grants the
// role actually holds: arrays of strings instead of booleans for every
// privilege. Postgres does the filtering server-side via a small CASE
// per privilege, then array_agg over the non-null results.
// ---------------------------------------------------------------------------

const clusterTmpl = `
-- The role may not exist yet (this is a valid state for the planner —
-- it'll emit CREATE ROLE). We gate every role-dependent expression on
-- a CTE-cached existence check so calls like has_database_privilege()
-- don't error out on a missing role.
WITH role_check AS (
    SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = {{ .RoleName | quoteLiteral }}) AS exists
)
SELECT jsonb_build_object(
    'role_name',   {{ .RoleName | quoteLiteral }}::text,
    'role_exists', (SELECT exists FROM role_check),
    'attributes',  CASE WHEN (SELECT exists FROM role_check) THEN COALESCE((
        SELECT jsonb_build_object(
            'SUPERUSER',   r.rolsuper,
            'INHERIT',     r.rolinherit,
            'CREATEROLE',  r.rolcreaterole,
            'CREATEDB',    r.rolcreatedb,
            'LOGIN',       r.rolcanlogin,
            'REPLICATION', r.rolreplication,
            'BYPASSRLS',   r.rolbypassrls
        )
        FROM pg_roles r WHERE r.rolname = {{ .RoleName | quoteLiteral }}
    ), '{}'::jsonb) ELSE '{}'::jsonb END,
    'memberships', CASE WHEN (SELECT exists FROM role_check) THEN COALESCE((
        SELECT jsonb_agg(grp.rolname ORDER BY grp.rolname)
        FROM pg_auth_members m
        JOIN pg_roles member ON member.oid = m.member
        JOIN pg_roles grp    ON grp.oid    = m.roleid
        WHERE member.rolname = {{ .RoleName | quoteLiteral }}
    ), '[]'::jsonb) ELSE '[]'::jsonb END,
    'databases_with_connect', CASE WHEN (SELECT exists FROM role_check) THEN COALESCE((
        SELECT jsonb_agg(datname ORDER BY datname)
        FROM pg_database
        WHERE NOT datistemplate
          AND has_database_privilege({{ .RoleName | quoteLiteral }}, datname, 'CONNECT')
    ), '[]'::jsonb) ELSE '[]'::jsonb END
)::text;
`

// Per-database template for the rollup model.
//
// For each schema in the database the template computes:
//   - bulk_privileges       privileges held on EVERY table (bool_and)
//   - table_count           number of tables (relkind r or p) in the schema
//   - exceptions.missing    {table: [privs]} for tables missing a bulk privilege
//   - exceptions.extra      {table: [privs]} for tables holding a non-bulk privilege
//
// Every non-system schema in the database is returned. Filtering for
// "interesting" scopes happens in Go after the rollup is computed.
//
// The whole query is gated on role-existence so a missing role yields
// empty privilege sets instead of erroring out from has_*_privilege.
const databaseTmpl = `
WITH role_check AS (
    SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = {{ .RoleName | quoteLiteral }}) AS exists
),
schema_filter AS (
    -- Every non-system schema in the database. Filtering for "is this
    -- scope interesting" happens in Go after the rollup is computed,
    -- because the criterion (non-empty bulk privileges OR mentioned in
    -- YAML) is easier to express there.
    SELECT n.nspname, n.oid
    FROM pg_namespace n
    WHERE n.nspname NOT LIKE 'pg_%'
      AND n.nspname <> 'information_schema'
),
-- One row per (schema, table). Used by bulk and exception math.
schema_tables AS (
    SELECT
        sf.oid       AS schema_oid,
        sf.nspname,
        c.oid        AS table_oid,
        c.relname    AS table_name
    FROM schema_filter sf
    LEFT JOIN pg_class c ON c.relnamespace = sf.oid AND c.relkind IN ('r','p')
),
-- For every (table, privilege_type) where the role currently holds the
-- grant, produce a row. NULL table_oid (empty schema) yields no rows
-- here. Skipped entirely when the role doesn't exist.
table_grants AS (
    SELECT st.schema_oid, st.nspname, st.table_oid, st.table_name, p.priv
    FROM schema_tables st
    CROSS JOIN (VALUES
        ('SELECT'), ('INSERT'), ('UPDATE'), ('DELETE'),
        ('TRUNCATE'), ('REFERENCES'), ('TRIGGER')
    ) AS p(priv)
    WHERE st.table_oid IS NOT NULL
      AND (SELECT exists FROM role_check)
      AND has_table_privilege({{ .RoleName | quoteLiteral }}, st.table_oid, p.priv)
),
-- Per-schema table count.
schema_stats AS (
    SELECT
        sf.nspname,
        sf.oid                  AS schema_oid,
        COUNT(st.table_oid)     AS table_count
    FROM schema_filter sf
    LEFT JOIN schema_tables st ON st.schema_oid = sf.oid AND st.table_oid IS NOT NULL
    GROUP BY sf.nspname, sf.oid
),
-- Bulk privileges: per schema, list of privileges held on every table.
-- Computed by counting (priv, schema) rows in table_grants and comparing
-- to the schema's table_count. Empty schemas have table_count = 0 and
-- yield an empty list.
schema_bulk AS (
    SELECT
        ss.schema_oid,
        ss.nspname,
        COALESCE((
            SELECT jsonb_agg(tg.priv ORDER BY tg.priv)
            FROM (
                SELECT priv, COUNT(*) AS n
                FROM table_grants
                WHERE schema_oid = ss.schema_oid
                GROUP BY priv
                HAVING COUNT(*) = ss.table_count AND ss.table_count > 0
            ) tg
        ), '[]'::jsonb) AS bulk
    FROM schema_stats ss
),
-- Per-(schema, table) the set of privileges the role holds, as a
-- jsonb array. Used for exception math below.
table_priv_sets AS (
    SELECT
        st.schema_oid,
        st.table_name,
        COALESCE(jsonb_agg(tg.priv ORDER BY tg.priv) FILTER (WHERE tg.priv IS NOT NULL), '[]'::jsonb) AS held
    FROM schema_tables st
    LEFT JOIN table_grants tg ON tg.table_oid = st.table_oid
    WHERE st.table_oid IS NOT NULL
    GROUP BY st.schema_oid, st.table_name
)
-- Final assembly. Each schema becomes one entry in the schemas array.
-- Exceptions are computed by comparing each table's held privileges to
-- the schema's bulk list.
SELECT jsonb_build_object(
    'database',    current_database(),
    'has_connect', CASE WHEN (SELECT exists FROM role_check)
                        THEN has_database_privilege({{ .RoleName | quoteLiteral }}, current_database(), 'CONNECT')
                        ELSE false END,
    'schemas',     COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
            'name',               ss.nspname,
            'has_usage',          CASE WHEN (SELECT exists FROM role_check)
                                       THEN has_schema_privilege({{ .RoleName | quoteLiteral }}, ss.schema_oid, 'USAGE')
                                       ELSE false END,
            'bulk_privileges',    sb.bulk,
            'table_count',        ss.table_count,
            'exceptions',         jsonb_build_object(
                'missing', COALESCE((
                    SELECT jsonb_object_agg(table_name, missing_list ORDER BY table_name)
                    FROM (
                        SELECT
                            tps.table_name,
                            (
                                SELECT jsonb_agg(b.value ORDER BY b.value)
                                FROM jsonb_array_elements_text(sb.bulk) b(value)
                                WHERE NOT (tps.held ? b.value)
                            ) AS missing_list
                        FROM table_priv_sets tps
                        WHERE tps.schema_oid = ss.schema_oid
                    ) m
                    WHERE m.missing_list IS NOT NULL AND jsonb_array_length(m.missing_list) > 0
                ), '{}'::jsonb),
                'extra', COALESCE((
                    SELECT jsonb_object_agg(table_name, extra_list ORDER BY table_name)
                    FROM (
                        SELECT
                            tps.table_name,
                            (
                                SELECT jsonb_agg(h.value ORDER BY h.value)
                                FROM jsonb_array_elements_text(tps.held) h(value)
                                WHERE NOT (sb.bulk ? h.value)
                            ) AS extra_list
                        FROM table_priv_sets tps
                        WHERE tps.schema_oid = ss.schema_oid
                    ) e
                    WHERE e.extra_list IS NOT NULL AND jsonb_array_length(e.extra_list) > 0
                ), '{}'::jsonb)
            )
        ) ORDER BY ss.nspname)
        FROM schema_stats ss
        JOIN schema_bulk sb ON sb.schema_oid = ss.schema_oid
    ), '[]'::jsonb)
)::text;
`

// ---------------------------------------------------------------------------
// Output struct. Two flat maps now — schema-level and table-level. The
// "database" tier collapses into the schema rows because CONNECT just
// rides along on every schema of that database.
// ---------------------------------------------------------------------------

type Snapshot struct {
	Role string `yaml:"role"`

	// RequiresMigration is true when apply.sql contains any non-comment
	// SQL — i.e., running it would change cluster state. False when
	// the role and all its scopes already match desired and apply.sql
	// is a `-- no changes` no-op.
	//
	// Narrow meaning by design: the flag answers "should I run
	// apply.sql?" Per-scope drift that the planner won't auto-fix
	// (status: out-of-sync/drifted) is visible in the per-scope
	// status field; it does NOT set this flag, because no migration
	// would resolve it — operator action is required instead.
	//
	// Populated by writePlan after building the SQL plan, so it's
	// always consistent with the apply.sql produced in the same run.
	RequiresMigration bool `yaml:"requires_migration"`

	Exists      bool            `yaml:"exists"`
	Attributes  map[string]bool `yaml:"attributes"`
	Memberships []string        `yaml:"memberships"`

	// ScopeStates is the per-scope state map, keyed by "<db>.<schema>".
	// One entry per scope the role manages (or has been asked about
	// via the YAML's `scopes:` list). Each value is a Scope describing
	// the rollup, the per-table exceptions, and the derived status.
	//
	// Named distinctly from the input `scopes:` field (Config.Scopes,
	// which is a list of paths the operator declared) to avoid the
	// ambiguity of "scopes" meaning both the input identifiers and
	// their observed states.
	ScopeStates map[string]Scope `yaml:"scope_states"`
}

// Scope captures the role's access pattern for one (database, schema)
// pair. The bulk_privileges field is the rollup of grants the role
// holds on every table in the schema; status is the high-level signal
// (`in-sync`, `out-of-sync/drifted`, `out-of-sync/unprovisioned`,
// `empty-schema`) and exceptions surfaces per-table deviations from
// that rollup.
//
// CONNECT and USAGE are tracked as internal-only fields (yaml:"-") so
// the planner can decide whether to emit GRANT statements for them,
// but they don't appear in current.yaml. This keeps the snapshot
// focused on managed state (privileges) while letting the planner
// avoid emitting redundant idempotent re-grants on every plan cycle.
type Scope struct {
	Connect        bool       `yaml:"-"`               // internal only
	Usage          bool       `yaml:"-"`               // internal only
	BulkPrivileges []string   `yaml:"bulk_privileges"` // privileges held on EVERY table
	TableCount     int        `yaml:"table_count"`
	Status         string     `yaml:"status"`
	Exceptions     Exceptions `yaml:"exceptions"`
}

// Exceptions holds the per-table deviations from the bulk pattern.
// `missing[<table>]` lists privileges that the role *should* have on
// that table (per bulk_privileges) but doesn't. `extra[<table>]` lists
// privileges that exist on that table but aren't in the bulk set.
type Exceptions struct {
	Missing map[string][]string `yaml:"missing"`
	Extra   map[string][]string `yaml:"extra"`
}

// ---------------------------------------------------------------------------
// Raw JSON shapes coming back from the templates.
// ---------------------------------------------------------------------------

type clusterJSON struct {
	RoleName             string          `json:"role_name"`
	RoleExists           bool            `json:"role_exists"`
	Attributes           map[string]bool `json:"attributes"`
	Memberships          []string        `json:"memberships"`
	DatabasesWithConnect []string        `json:"databases_with_connect"`
}

type databaseJSON struct {
	Database   string       `json:"database"`
	HasConnect bool         `json:"has_connect"`
	Schemas    []schemaJSON `json:"schemas"`
}

// schemaJSON is the per-scope rollup result. Tables in the schema are
// not returned individually — they're collapsed into bulk_privileges
// (computed via bool_and per privilege type) and exceptions (only
// listed when bool_and is false).
type schemaJSON struct {
	Name           string         `json:"name"`
	HasUsage       bool           `json:"has_usage"`
	BulkPrivileges []string       `json:"bulk_privileges"`
	TableCount     int            `json:"table_count"`
	Exceptions     exceptionsJSON `json:"exceptions"`
}

type exceptionsJSON struct {
	Missing map[string][]string `json:"missing"`
	Extra   map[string][]string `json:"extra"`
}

// ---------------------------------------------------------------------------
// psql runner.
// ---------------------------------------------------------------------------

func runPsql(conn, sql string) ([]byte, error) {
	cmd := exec.Command("psql",
		"--no-align",
		"--tuples-only",
		"--quiet",
		"--no-psqlrc",
		"--set", "ON_ERROR_STOP=1",
		"--dbname", conn,
	)
	cmd.Env = append(cmd.Env, "PGCONNECT_TIMEOUT=5")
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(sql)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("psql: %w: %s", err, stderr.String())
	}
	return bytes.TrimSpace(stdout.Bytes()), nil
}

// ---------------------------------------------------------------------------
// Snapshot construction. Cluster query plus optional per-database fan-out.
// ---------------------------------------------------------------------------

// SnapshotRequest collects the inputs takeSnapshot needs into one
// struct so the call site reads as named fields rather than positional
// arguments. It also makes future additions (timeouts, cancellation
// contexts, output controls) cheap.
//
// This is managed-mode only. External mode calls clusterSnapshot
// directly because it doesn't need the per-database fan-out.
type SnapshotRequest struct {
	// Conn holds the parsed connection components (user, password,
	// host, port, default db, options). Each per-database query
	// rebuilds the URI from these parts; the cluster query connects
	// to Conn.DefaultDB.
	Conn ConnectionParts
	// Role is the Postgres role name to snapshot (e.g.,
	// "hoopdev_inventory_ro"). The query gates all role-dependent
	// expressions on this name's existence in pg_roles.
	Role string
	// Schemas is a map from database name to a list of schema names
	// the YAML asked about. Used for the "interesting" filter so
	// newly-configured scopes get included in the snapshot even when
	// the role doesn't yet have grants on them.
	Schemas map[string][]string
}

// clusterSnapshot runs the cluster-level query and returns a Snapshot
// populated with the role's existence, attributes, and memberships —
// everything the cluster query reveals. The Scopes map is initialized
// to empty; populating it requires the per-database fan-out, which
// happens in takeSnapshot.
//
// The second return value is the list of databases the role currently
// has CONNECT on (directly or transitively via PUBLIC). takeSnapshot
// uses this as one input to the fan-out target list; external mode
// ignores it.
//
// Both managed and external modes call this. It's the shared cluster-
// level read; mode-specific work happens in the callers.
func clusterSnapshot(conn ConnectionParts, role string) (*Snapshot, []string, error) {
	clusterSQL, err := render("cluster", clusterTmpl, struct{ RoleName string }{role})
	if err != nil {
		return nil, nil, err
	}
	raw, err := runPsql(conn.connURI(conn.DefaultDB), clusterSQL)
	if err != nil {
		return nil, nil, fmt.Errorf("cluster query: %w", err)
	}
	var cj clusterJSON
	if err := json.Unmarshal(raw, &cj); err != nil {
		return nil, nil, fmt.Errorf("decode cluster JSON: %w (raw=%s)", err, raw)
	}

	snap := &Snapshot{
		Role:        cj.RoleName,
		Exists:      cj.RoleExists,
		Attributes:  cj.Attributes,
		Memberships: cj.Memberships,
		ScopeStates: map[string]Scope{},
	}
	if snap.Memberships == nil {
		snap.Memberships = []string{}
	}
	return snap, cj.DatabasesWithConnect, nil
}

// takeSnapshot is the managed-mode snapshot: cluster query plus
// per-database fan-out. The Scopes map is populated with one entry
// per (db, schema) the role has access to or has been asked about
// via req.Schemas.
//
// External mode does not call this. Calling clusterSnapshot directly
// avoids the fan-out — which for an external-mode role would mean
// opening a connection to every database the parent transitively
// grants CONNECT to via PUBLIC membership. Wasted work, since
// external mode doesn't manage privilege state.
func takeSnapshot(req SnapshotRequest) (*Snapshot, error) {
	snap, dbsWithConnect, err := clusterSnapshot(req.Conn, req.Role)
	if err != nil {
		return nil, err
	}

	// Per-database fan-out. Targets are the union of:
	//   - databases the role can CONNECT to (existing managed scopes)
	//   - databases listed in req.Schemas (newly-configured scopes that
	//     might exist in the cluster but the role can't yet reach)
	dbsSeen := map[string]struct{}{}
	for _, db := range dbsWithConnect {
		dbsSeen[db] = struct{}{}
	}
	for db := range req.Schemas {
		dbsSeen[db] = struct{}{}
	}
	dbs := make([]string, 0, len(dbsSeen))
	for db := range dbsSeen {
		dbs = append(dbs, db)
	}
	sort.Strings(dbs)

	for _, db := range dbs {
		dbSQL, err := render("database", databaseTmpl, struct {
			RoleName string
		}{req.Role})
		if err != nil {
			return nil, err
		}
		conn := req.Conn.connURI(db)
		raw, err := runPsql(conn, dbSQL)
		if err != nil {
			return nil, fmt.Errorf("database %q: %w", db, err)
		}
		var dj databaseJSON
		if err := json.Unmarshal(raw, &dj); err != nil {
			return nil, fmt.Errorf("decode database %q JSON: %w (raw=%s)", db, err, raw)
		}

		for _, s := range dj.Schemas {
			schemaKey := dj.Database + "." + s.Name
			scope := Scope{
				Connect:        dj.HasConnect,
				Usage:          s.HasUsage,
				BulkPrivileges: s.BulkPrivileges,
				TableCount:     s.TableCount,
			}
			if scope.BulkPrivileges == nil {
				scope.BulkPrivileges = []string{}
			}

			// Status is derived. Four values:
			//   - empty-schema:               schema has zero tables, rollup undefined
			//   - out-of-sync/unprovisioned:  schema has tables but the role holds nothing
			//   - out-of-sync/drifted:        some tables deviate from the bulk pattern
			//   - in-sync:                    rollup describes the access pattern uniformly
			//
			// Both exception maps are always populated (non-nil) so the
			// YAML output is shape-uniform: every scope renders with
			// `exceptions: {missing: {...}, extra: {...}}`, even when
			// the maps are empty. yaml.v3 marshals nil maps as `null`,
			// which would break that uniformity.
			missing := s.Exceptions.Missing
			if missing == nil {
				missing = map[string][]string{}
			}
			extra := s.Exceptions.Extra
			if extra == nil {
				extra = map[string][]string{}
			}
			hasExceptions := len(missing) > 0 || len(extra) > 0
			switch {
			case s.TableCount == 0:
				// Schema has no tables. The rollup is undefined for
				// the trivial reason: there's nothing to roll up over.
				// Could be a freshly-created schema awaiting its first
				// table, or just an unused namespace.
				scope.Status = "empty-schema"
			case hasExceptions:
				// Some tables hold different privileges than others.
				// `bulk_privileges` reflects what's held uniformly (may
				// be empty if no privilege is uniform across all tables);
				// `exceptions` lists the specific deviations. This case
				// MUST be checked before the empty-bulk case below
				// because an empty bulk with exceptions means "some
				// tables have privs, others don't" — there ARE grants,
				// just not uniform.
				scope.Status = "out-of-sync/drifted"
			case len(s.BulkPrivileges) == 0:
				// Schema has tables, no exceptions surface, and bulk
				// is empty: the role genuinely holds no privileges on
				// any table in this schema. Most often this is a
				// non-existent role, or one that was fully revoked.
				// The next plan run will likely emit GRANT statements
				// that bring the role to in-sync.
				scope.Status = "out-of-sync/unprovisioned"
			default:
				// Schema has tables, role has uniform bulk privileges,
				// no per-table deviations. The rollup describes the
				// access pattern accurately.
				scope.Status = "in-sync"
			}
			scope.Exceptions = Exceptions{Missing: missing, Extra: extra}

			// Interestingness filter. A scope earns a place in the
			// snapshot only if there's something to manage:
			//
			//   - non-empty bulk_privileges (the role holds real grants)
			//   - the YAML asked about it (the user may want to plan it)
			//
			// Without this filter, the snapshot would include every
			// schema in every database the role can connect to via the
			// implicit PUBLIC membership — typically dozens or hundreds
			// of empty scopes that produce no SQL but flood the diff
			// with noise.
			interesting := len(scope.BulkPrivileges) > 0 ||
				inDesired(dj.Database, s.Name, req.Schemas)
			if !interesting {
				continue
			}

			snap.ScopeStates[schemaKey] = scope
		}
	}

	return snap, nil
}

// inDesired returns true if the user's YAML asked for this (db, schema)
// pair via the schemas-of-interest map.
func inDesired(db, schema string, schemas map[string][]string) bool {
	for _, s := range schemas[db] {
		if s == schema {
			return true
		}
	}
	return false
}

// checkParentExists is the external-mode precondition: the parent
// role named by source_role must already exist in the cluster
// before we can create children that inherit from it. Returns an
// error with a clear message if the parent is missing.
func checkParentExists(conn ConnectionParts, parent string) error {
	sql := fmt.Sprintf(
		`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = %s)::text;`,
		quoteLiteral(parent),
	)
	out, err := runPsql(conn.connURI(conn.DefaultDB), sql)
	if err != nil {
		return fmt.Errorf("check parent role %q: %w", parent, err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("parent role %q does not exist (required by source_role); "+
			"create it via your usual provisioning before running it", parent)
	}
	return nil
}
