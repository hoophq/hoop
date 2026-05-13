package pgmanager

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"gopkg.in/yaml.v3"
)

const defaultPostgresDatabase string = "postgres"

type planResponse struct {
	RoleExists      bool
	StateMigration  []byte
	SQLPlanChecksum string
	Status          string
}

type StateMigration struct {
	Config          *Config   `yaml:"config"`
	CurrentState    *Snapshot `yaml:"current_state"`
	SQLPlan         yaml.Node `yaml:"sql_plan"`
	SQLPlanChecksum string    `yaml:"sql_plan_checksum"`
	CommandOutput   string    `yaml:"command_output"`
}

func ProcessPlanRequest(client pb.ClientTransport, pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	var req pbsystem.PgManagerPlanRequest
	if err := yaml.Unmarshal(pkt.Payload, &req); err != nil {
		sendPlanResponse(client, newPlanError(sid, "failed unmarshaling plan: %v", err))
		return
	}

	connParts := ConnectionParts{
		Host:      req.Host,
		Port:      req.Port,
		User:      req.MasterUser,
		Password:  req.MasterPwd,
		DefaultDB: defaultPostgresDatabase,
		Options:   req.Options,
	}

	config := &Config{
		Type:           req.Type,
		RoleName:       req.RoleName,
		Scopes:         req.Scopes,
		Privileges:     req.Privileges,
		RotatePassword: req.RotatePassword,
	}
	var resp *planResponse
	var err error
	switch req.Type {
	case typeExternal:
		resp, err = planExternal(config, connParts)
	case typeManaged:
		resp, err = planManaged(config, connParts)
	default:
		sendPlanResponse(client, newPlanError(sid, "invalid plan type: %q", req.Type))
		return
	}

	if err != nil {
		sendPlanResponse(client, newPlanError(sid, "failed performing plan: %v", err))
		return
	}

	sendPlanResponse(client, &pbsystem.PgManagerPlanResponse{
		SID:            sid,
		StateMigration: resp.StateMigration,
		Status:         resp.Status,
		Message:        "",
	})
}

func ProcessApplyPlan(client pb.ClientTransport, pkt *pb.Packet) {
	sid := string(pkt.Spec[pb.SpecGatewaySessionID])
	var req pbsystem.PgManagerApplyRequest
	if err := yaml.Unmarshal(pkt.Payload, &req); err != nil {
		sendApplyResponse(client, newApplyError(sid, "failed unmarshaling apply: %v", err))
		return
	}
	var migState StateMigration
	err := yaml.Unmarshal(req.StateMigration, &migState)
	if err != nil {
		sendApplyResponse(client, newApplyError(sid, "failed unmarshaling state migration: %v", err))
		return
	}

	connParts := ConnectionParts{
		Host:      req.Host,
		Port:      req.Port,
		User:      req.MasterUser,
		Password:  req.MasterPwd,
		DefaultDB: defaultPostgresDatabase,
		Options:   req.Options,
	}

	if migState.Config == nil {
		sendApplyResponse(client, newApplyError(sid, "state migration missing config (nil)"))
		return
	}

	var resp *planResponse
	switch migState.Config.Type {
	case typeExternal:
		resp, err = planExternal(migState.Config, connParts)
	case typeManaged:
		resp, err = planManaged(migState.Config, connParts)
	default:
		sendApplyResponse(client, newApplyError(sid, "invalid plan type: %q", migState.Config.Type))
		return
	}

	if err != nil {
		sendApplyResponse(client, newApplyError(sid, "failed performing plan-apply: %v", err))
		return
	}

	if resp.SQLPlanChecksum != migState.SQLPlanChecksum {
		sendApplyResponse(client, newApplyError(sid, "sql plan mismatch, got=%v, wanted=%v",
			resp.SQLPlanChecksum, migState.SQLPlanChecksum))
		return
	}

	var rolePwd string
	sqlPlan := migState.SQLPlan.Value
	if migState.Config.RotatePassword || !migState.CurrentState.Exists {
		rolePwd, err = randomPassword()
		if err != nil {
			sendApplyResponse(client, newApplyError(sid, "failed generating password: %v", err))
			return
		}
		sqlPlan = strings.ReplaceAll(sqlPlan, "ROLE_PASSWORD_PLACEHOLDER", rolePwd)
	}

	cmdOutput, err := runPsql(connParts.connURI(connParts.DefaultDB), sqlPlan)
	if err != nil {
		sendApplyResponse(client, newApplyError(sid, "failed running apply with psql: %v", err))
		return
	}

	migState.CommandOutput = string(cmdOutput)
	stateMigrationBytes, err := yaml.Marshal(migState)
	if err != nil {
		log.With("sid", sid).Warnf("failed re-encoding migration state with output: %v", err)
	}
	sendApplyResponse(client, &pbsystem.PgManagerApplyResponse{
		SID:            sid,
		StateMigration: stateMigrationBytes,
		Status:         "success",
		Message:        "",
		RoleName:       migState.Config.RoleName,
		RolePassword:   rolePwd,
	})

}

func sendPlanResponse(client pb.ClientTransport, resp *pbsystem.PgManagerPlanResponse) {
	payload, pbType, err := resp.Encode()
	if err != nil {
		log.With("sid", resp.SID).Warnf("failed encoding plan response: %v", err)
		return
	}
	if err := client.Send(&pb.Packet{
		Type:    pbType,
		Payload: payload,
		Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(resp.SID)},
	}); err != nil {
		log.With("sid", resp.SID).Warnf("failed sending plan response: %v", err)
	}
}

func sendApplyResponse(client pb.ClientTransport, resp *pbsystem.PgManagerApplyResponse) {
	payload, pbType, err := resp.Encode()
	if err != nil {
		log.With("sid", resp.SID).Warnf("failed encoding apply response: %v", err)
		return
	}
	if err := client.Send(&pb.Packet{
		Type:    pbType,
		Payload: payload,
		Spec:    map[string][]byte{pb.SpecGatewaySessionID: []byte(resp.SID)},
	}); err != nil {
		log.With("sid", resp.SID).Warnf("failed sending apply response: %v", err)
	}
}

func newPlanError(sid, format string, a ...any) *pbsystem.PgManagerPlanResponse {
	return pbsystem.NewPgManagerPlanError(sid, format, a...)
}

func newApplyError(sid, format string, a ...any) *pbsystem.PgManagerApplyResponse {
	return pbsystem.NewPgManagerApplyError(sid, format, a...)
}

// expandManaged turns a managed-mode YAML config into a desired-state
// Snapshot under the rollup model. Each scope listed in `scopes:` becomes
// one entry in the ScopeStates map, with CONNECT, USAGE, the requested
// privileges as the bulk pattern, and the same privileges as the
// default-privileges contract for new tables.
//
// Note that table_count, status, and exceptions are NOT part of the
// desired state — they're catalog facts the planner reads from the
// live cluster. Only "what should be true about the role" lives here.
func expandManaged(c *Config) *Snapshot {
	desired := &Snapshot{
		Role:        c.RoleName,
		Exists:      true,
		Attributes:  defaultAttributes(),
		Memberships: []string{},
		ScopeStates: map[string]Scope{},
	}

	// Sort & uppercase the privilege list so the bulk and default
	// privileges have a stable, canonical order.
	privs := make([]string, 0, len(c.Privileges))
	for _, p := range c.Privileges {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p != "" {
			privs = append(privs, p)
		}
	}
	sort.Strings(privs)

	for _, p := range c.Scopes {
		db, schema, _ := splitPath(p)
		if db == "" {
			continue
		}
		schemaKey := db + "." + schema
		desired.ScopeStates[schemaKey] = Scope{
			Connect:        true,
			Usage:          true,
			BulkPrivileges: privs,
			// Connect/Usage are internal-only (yaml:"-"), tracked so
			// the planner can skip redundant idempotent re-grants;
			// they don't appear in the snapshot YAML.
			//
			// table_count / status / exceptions are catalog facts —
			// they don't appear in the desired state.
		}
	}

	return desired
}

type Config struct {
	Type           string   `yaml:"type"`
	RoleName       string   `yaml:"role_name"`
	Scopes         []string `yaml:"scopes"`
	Privileges     []string `yaml:"privileges"`
	RotatePassword bool     `yaml:"rotate_password"`
}

// planManaged runs the full reconcile flow for a type=managed role.
// The role exists end-to-end here: snapshot the cluster, verify each
// declared scope exists in the catalog, expand the YAML into a
// desired Snapshot, diff, write outputs.
//
// Distinguishing characteristic: per-database fan-out and bulk
// privilege reconciliation. The snapshot connects to every database
// the role can reach (or the YAML asks about), reads catalog state
// per-schema, and the planner emits GRANT/REVOKE statements scoped
// at "ALL TABLES IN SCHEMA …" granularity.
func planManaged(config *Config, conn ConnectionParts) (*planResponse, error) {
	current, err := takeSnapshot(SnapshotRequest{
		Conn:    conn,
		Role:    config.RoleName,
		Schemas: schemasOfInterest(config.Scopes),
	})
	if err != nil {
		return nil, fmt.Errorf("failed taking snapshot: %w", err)
	}

	// Precondition: each declared scope must exist in the cluster
	// (database AND schema present). With "missing schema → error"
	// as the chosen policy, surface it before generating SQL that
	// would silently no-op.
	for _, p := range config.Scopes {
		db, schema, _ := splitPath(p)
		if db == "" {
			continue
		}
		key := db + "." + schema
		if _, ok := current.ScopeStates[key]; !ok {
			return nil, fmt.Errorf("scope %q does not exist in the cluster", p)
		}
	}

	desired := expandManaged(config)
	return buildPlan(config, current, desired)
}

// planExternal runs the reconcile flow for a type=external role. The
// role inherits all privileges from inherits_from via membership;
// it only manages the role's existence, password, and the parent
// membership.
//
// Distinguishing characteristic: NO per-database fan-out. We don't
// connect to every database the parent role transitively grants
// CONNECT to via PUBLIC — that would be wasted work since we don't
// touch any privilege state below the cluster level. The cluster
// query alone gives us role existence and current memberships, which
// is all we need.
func planExternal(c *Config, conn ConnectionParts) (*planResponse, error) {
	// Precondition: the parent role must already exist in the cluster
	// before we can create children that inherit from it. Fail loudly
	// with a useful message rather than emitting SQL that will fail
	// at apply time.
	if err := checkParentExists(conn, c.RoleName); err != nil {
		return nil, fmt.Errorf("parent role %q does not exist in the cluster: %w", c.RoleName, err)
	}

	current, _, err := clusterSnapshot(conn, c.RoleName)
	if err != nil {
		return nil, fmt.Errorf("failed taking cluster snapshot: %w", err)
	}

	// builds the desired state for an external-mode config.
	// Privileges are out of scope — the role inherits everything from
	// roleName via membership with INHERIT. The desired snapshot
	// asserts only that the role exists and is a member of the parent.
	desired := &Snapshot{
		Role:        c.RoleName,
		Exists:      true,
		Attributes:  defaultAttributes(),
		Memberships: []string{c.RoleName},
		ScopeStates: map[string]Scope{},
	}
	return buildPlan(c, current, desired)
}

// buildPlan is the shared output tail. Both modes produce the same
// pair of files (current.yaml, apply.sql) in the same shape, so the
// final marshal-and-write step is factored out. Returns the exit
// code for cmdPlan to propagate.
//
// Order matters: the SQL plan is built first so we can set
// current.RequiresMigration before marshaling current.yaml. That way
// the flag in the YAML and the contents of apply.sql are guaranteed
// consistent — true iff there's something to apply.
func buildPlan(config *Config, current, desired *Snapshot) (*planResponse, error) {
	plan, err := buildSQLPlan(config.RotatePassword, desired, current)
	if err != nil {
		return nil, fmt.Errorf("failed building SQL plan: %w", err)
	}

	sqlPlanStr, err := plan.Render()
	if err != nil {
		return nil, err
	}

	hasChanges := !plan.IsEmpty()
	current.RequiresMigration = hasChanges
	status := "in-sync"
	if hasChanges {
		status = "out-of-sync"
	}

	sqlPlanChecksum, err := checksumSha256([]byte(sqlPlanStr))
	if err != nil {
		return nil, fmt.Errorf("failed generating state migration checksum: %v", err)
	}
	stateMigrationYaml, err := yaml.Marshal(&StateMigration{
		Config:          config,
		CurrentState:    current,
		SQLPlanChecksum: sqlPlanChecksum,
		SQLPlan: yaml.Node{
			Kind:  yaml.ScalarNode,
			Style: yaml.LiteralStyle,
			Value: sqlPlanStr,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed marshaling response payload: %w", err)
	}

	return &planResponse{
		StateMigration:  stateMigrationYaml,
		SQLPlanChecksum: sqlPlanChecksum,
		Status:          status,
		RoleExists:      current.Exists,
	}, nil
}
