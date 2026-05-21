package pbsystem

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	PgManagerPlanRequestType  string = "SysPgManagerPlanRequest"
	PgManagerPlanResponseType string = "SysPgManagerPlanResponse"

	PgManagerApplyRequestType  string = "SysPgManagerApplyRequest"
	PgManagerApplyResponseType string = "SysPgManagerApplyResponse"
)

type PgCredentials struct {
	MasterUser string            `yaml:"master_user"`
	MasterPwd  string            `yaml:"master_pwd"`
	Host       string            `yaml:"host"`
	Port       string            `yaml:"port"`
	Options    map[string]string `yaml:"options"`
}

type PgManagerPlanRequest struct {
	SID string `yaml:"sid"`

	// RoleName is the name of the role to manage.
	// For type=managed, the planner ensures the role exists and
	// has the specified privileges on the specified scopes.
	RoleName string `yaml:"role_name"`

	// SourceRole is the name of an existent role that will be used
	// to grant the permission to role name.
	SourceRole string `yaml:"source_role"`

	//	type: managed   — the tool fully owns the role's grants. scopes
	//	                  and privileges describe what the role should hold;
	//	                  the planner reconciles drift in either direction.
	//	                  This is the default if `type` is omitted.
	//
	//	type: external  — the role inherits all privileges from an existing
	//	                  role (source_role) via Postgres GRANT … TO …
	//	                  membership with INHERIT. The tool only manages
	//	                  the child role's existence, password, and the
	//	                  parent membership. Privileges themselves are
	//	                  out of scope.
	Type string `yaml:"type,omitempty"` // "managed" (default) or "external"

	// Scopes are the list of database and schema paths the role should have privileges on.
	// Paths are dot-separated strings of the form "<db>" or "<db>.<schema>".
	// The planner derives the full list of tables in scope per database.
	Scopes []string `yaml:"scopes,omitempty"` // managed mode only
	// The list of privileges the role should have on each table in scope, e.g. ["SELECT", "INSERT"].
	Privileges []string `yaml:"privileges,omitempty"` // managed mode only

	// RotatePassword, when true, asks pgdiff to emit
	// `ALTER ROLE … PASSWORD '<new random>'` on the next plan run.
	// Model A: one-shot trigger that the operator clears manually.
	//
	// Workflow:
	//   1. Password is broken / lost / compromised.
	//   2. Operator sets rotate_password: true.
	//   3. Plan + apply: new password appears in apply.sql.
	//   4. Operator flips rotate_password back to false and commits.
	//
	// If step 4 is forgotten, the next plan emits another rotation
	// (a different random password each run). That's a deliberate
	// safety property: leaving the flag stuck produces a visible
	// signal — the apply.sql keeps offering new passwords — rather
	// than a silent no-op.
	//
	// Rotation is a separate concept from initial provisioning.
	// Setting rotate_password: true when the role doesn't exist yet
	// is rejected: the role's first password rides along with CREATE
	// ROLE, and asking to rotate a password that doesn't exist is
	// almost certainly a YAML mistake.
	RotatePassword bool `yaml:"rotate_password"`

	// Admin connection info for the Postgres cluster.
	// The planner uses this to connect and inspect the cluster state, and to emit GRANT statements
	// for external roles. For managed roles the planner only emits per-table
	// GRANTs, so the admin connection's permissions can be scoped accordingly.

	PgCredentials `yaml:",inline"`
}

type PgManagerPlanResponse struct {
	SID            string `yaml:"sid"`
	StateMigration []byte `yaml:"state_migration"`
	Status         string `yaml:"status"`
	Message        string `yaml:"message"`
}

func (r *PgManagerPlanResponse) Encode() ([]byte, string, error) {
	payload, err := yaml.Marshal(r)
	if err != nil {
		return nil, "", err
	}
	return payload, PgManagerPlanResponseType, nil
}

type PgManagerApplyRequest struct {
	SID            string `yaml:"sid"`
	StateMigration []byte `yaml:"state_migration"`

	PgCredentials `yaml:",inline"`
}

type PgManagerApplyResponse struct {
	SID            string `yaml:"sid"`
	StateMigration []byte `yaml:"state_migration"`
	Status         string `yaml:"status"`
	Message        string `yaml:"message"`
	RoleName       string `yaml:"role_name"`
	RolePassword   string `yaml:"password"`
}

func (r *PgManagerApplyResponse) Encode() ([]byte, string, error) {
	payload, err := yaml.Marshal(r)
	if err != nil {
		return nil, "", err
	}
	return payload, PgManagerPlanResponseType, nil
}

func NewPgManagerPlanError(sid, format string, a ...any) *PgManagerPlanResponse {
	return &PgManagerPlanResponse{
		SID:     sid,
		Status:  "failed",
		Message: fmt.Sprintf(format, a...),
	}
}

func NewPgManagerApplyError(sid, format string, a ...any) *PgManagerApplyResponse {
	return &PgManagerApplyResponse{
		SID:     sid,
		Status:  "failed",
		Message: fmt.Sprintf(format, a...),
	}
}

func NewPgManagerPlanRequest(req *PgManagerPlanRequest) ([]byte, string, error) {
	payload, err := yaml.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	return payload, PgManagerPlanRequestType, nil
}

func NewPgManagerApplyRequest(req *PgManagerApplyRequest) ([]byte, string, error) {
	payload, err := yaml.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	return payload, PgManagerApplyRequestType, nil
}
