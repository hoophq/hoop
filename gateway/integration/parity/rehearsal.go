//go:build integration && parity

package parity

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/hoophq/hoop/common/proto"
	migrationfiles "github.com/hoophq/hoop/gateway/migrations"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
)

// The migration-rehearsal run boots the control plane on a synthetic copy of
// a control-plane database. No customer database is available, so the copy
// is built here: the embedded migrations up to rehearsalVersion, then rows in
// the shape a control plane wrote them at that version.

// rehearsalVersion is the last schema before the sidecar-in-gateway
// migrations (000128 and 000129, PR #1907).
const rehearsalVersion = 127

// rehearsalSidecar is one seeded sidecar. Token is the raw token; the row
// stores its SHA-256 in hex, as models.HashAPIKey writes it.
type rehearsalSidecar struct {
	ID, Name, Token, CreatedBy string
	CreatedAt                  time.Time
	Config                     string
	// Runtime state from the last handshake before the copy was taken.
	ReportedVersion, ServedRevision, AppliedRevision, LastOutcome, LastError string
	LastSeenAt                                                               time.Time
	Capabilities                                                             []string
}

// rehearsalListener is one listener and the mirror connection the boot must
// give it. Fallback marks a listener whose preferred name <sidecar>-<listener>
// cannot be used, so its mirror takes models.SidecarMirrorFallbackName.
type rehearsalListener struct {
	Sidecar, Name, Type, Subtype string
	Fallback                     bool
}

// rehearsalBinding is one rule bound to one listener at one position.
type rehearsalBinding struct {
	Kind, Rule, Sidecar, Listener string
	Position                      int
}

// rehearsalRule is one rule row: kind is guardrail, datamasking or analyzer,
// spec is sidecar_spec, the block the listener receives.
type rehearsalRule struct {
	Kind, Name, Spec string
}

type rehearsalReview struct {
	ID, SessionID, Sidecar, Listener, Rule, Status, StatementHash string
	RejectionReason                                               string
	CreatedAt                                                     time.Time
}

type rehearsalSlackChannels struct {
	Sidecar, Listener string
	Channels          []string
}

const (
	rehearsalOrgID   = "5eed0000-0000-4000-8000-000000000001"
	rehearsalCreator = "platform-admin@acme.test"
	// rehearsalCollision is an ordinary connection whose name is the
	// preferred mirror name of crm's appdb listener.
	rehearsalCollision = "crm-appdb"
	// rehearsalApprovalRule is the sidecar access request rule the payments
	// appdb analyzer holds statements for.
	rehearsalApprovalRule = "payments-approvers"
	// rehearsalHandshakeVersion is a release that decodes every field and
	// speaks every protocol the seeded documents use.
	rehearsalHandshakeVersion = "1.210.0"
)

var rehearsalBase = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

var rehearsalSidecars = []rehearsalSidecar{
	{
		ID: "5eed0000-0000-4000-8000-0000000000a1", Name: "payments", Token: "hsc_parity_rehearsal_payments_7f3c1a",
		CreatedBy: rehearsalCreator, CreatedAt: rehearsalBase,
		Config: `{"listeners":[
			{"name":"appdb","protocol":"postgres","listen":"0.0.0.0:15432","upstream":"payments-db.internal:5432",
			 "analyzer":{"max_calls":40,"fail_open":false}},
			{"name":"ledger","protocol":"mysql","listen":"0.0.0.0:13306","upstream":"ledger-db.internal:3306"},
			{"name":"billing","protocol":"mssql","listen":"0.0.0.0:11433","upstream":"billing-db.internal:1433"},
			{"name":"events","protocol":"mongodb","listen":"0.0.0.0:17017","upstream":"events-db.internal:27017"}],
			"analyzer":{"provider":"anthropic","model":"claude-sonnet-4-5","credentials_file":"/etc/hoop-inspect/anthropic.key"}}`,
		ReportedVersion: "1.205.0", ServedRevision: "rev-payments-0001", AppliedRevision: "rev-payments-0001",
		LastOutcome: "applied", LastSeenAt: rehearsalBase.Add(72 * time.Hour),
		Capabilities: []string{"protocol:mongodb", "protocol:mssql", "protocol:mysql", "protocol:postgres"},
	},
	{
		ID: "5eed0000-0000-4000-8000-0000000000a2", Name: "edge", Token: "hsc_parity_rehearsal_edge_91be04",
		CreatedBy: rehearsalCreator, CreatedAt: rehearsalBase.Add(time.Hour),
		Config: `{"listeners":[
			{"name":"bastion","protocol":"ssh","listen":"0.0.0.0:2222",
			 "ssh":{"host_key":"/etc/hoop-inspect/ssh_host_ed25519_key","trusted_ca":"/etc/hoop-inspect/trusted_ca.pub",
			        "destinations_allowed":["10.30.10.0/24:22"],"identity":{"subject":"key_id","groups":"principals"}}},
			{"name":"api","protocol":"http","listen":"0.0.0.0:18080","upstream":"orders-api.internal:80"},
			{"name":"metrics","protocol":"clickhouse","listen":"0.0.0.0:19000","upstream":"clickhouse.internal:9000"},
			{"name":"orders","protocol":"grpc","listen":"0.0.0.0:15051","upstream":"orders-grpc.internal:50051"}]}`,
		ReportedVersion: "1.207.0", ServedRevision: "rev-edge-0003", AppliedRevision: "rev-edge-0002",
		LastOutcome: "refused", LastError: "listener \"metrics\": upstream unreachable at boot",
		LastSeenAt:   rehearsalBase.Add(73 * time.Hour),
		Capabilities: []string{"protocol:clickhouse", "protocol:grpc", "protocol:http", "protocol:ssh"},
	},
	{
		// appdb composes to a name an ordinary connection already has, and
		// "reporting replica" composes to one the connection name rule
		// refuses: both mirrors take the fallback name.
		ID: "5eed0000-0000-4000-8000-0000000000a3", Name: "crm", Token: "hsc_parity_rehearsal_crm_c0ffee",
		CreatedBy: rehearsalCreator, CreatedAt: rehearsalBase.Add(2 * time.Hour),
		Config: `{"listeners":[
			{"name":"appdb","protocol":"postgres","listen":"0.0.0.0:25432","upstream":"crm-db.internal:5432"},
			{"name":"reporting replica","protocol":"postgres","listen":"0.0.0.0:25433","upstream":"crm-replica.internal:5432"}]}`,
		ReportedVersion: "1.205.0", ServedRevision: "rev-crm-0001", AppliedRevision: "rev-crm-0001",
		LastOutcome: "unchanged", LastSeenAt: rehearsalBase.Add(74 * time.Hour),
		Capabilities: []string{"protocol:postgres"},
	},
	{
		// Runs its own config file. No check handshakes it, so its runtime
		// state must read back exactly as seeded.
		ID: "5eed0000-0000-4000-8000-0000000000a4", Name: "legacy", Token: "hsc_parity_rehearsal_legacy_5a5a5a",
		CreatedBy: "ops@acme.test", CreatedAt: rehearsalBase.Add(3 * time.Hour),
		Config: `{"load_from_disk":true,"listeners":[
			{"name":"inventory","protocol":"spanner","listen":"0.0.0.0:19010","upstream":"spanner.googleapis.com:443"}]}`,
		ReportedVersion: "1.198.0", LastOutcome: "applied", LastSeenAt: rehearsalBase.Add(75 * time.Hour),
		Capabilities: []string{"protocol:spanner"},
	},
}

var rehearsalListeners = []rehearsalListener{
	{Sidecar: "payments", Name: "appdb", Type: "database", Subtype: "postgres"},
	{Sidecar: "payments", Name: "ledger", Type: "database", Subtype: "mysql"},
	{Sidecar: "payments", Name: "billing", Type: "database", Subtype: "mssql"},
	{Sidecar: "payments", Name: "events", Type: "database", Subtype: "mongodb"},
	{Sidecar: "edge", Name: "bastion", Type: "application", Subtype: "ssh"},
	{Sidecar: "edge", Name: "api", Type: "httpproxy", Subtype: "httpproxy"},
	{Sidecar: "edge", Name: "metrics", Type: "custom", Subtype: "clickhouse"},
	{Sidecar: "edge", Name: "orders", Type: "custom", Subtype: "grpc"},
	{Sidecar: "crm", Name: "appdb", Type: "database", Subtype: "postgres", Fallback: true},
	{Sidecar: "crm", Name: "reporting replica", Type: "database", Subtype: "postgres", Fallback: true},
	{Sidecar: "legacy", Name: "inventory", Type: "custom", Subtype: "spanner"},
}

// The rule names sort against their positions on purpose: the served order
// must follow position, never name.
var rehearsalRules = []rehearsalRule{
	{"guardrail", "pay-zz-no-destructive",
		`{"rules":[{"name":"no-destructive-sql","type":"operation","operations":["drop","truncate"],"message":"ask the data team"}]}`},
	{"guardrail", "pay-aa-read-only-customers",
		`{"rules":[{"name":"read-only-customers","type":"table","tables":["customers"],"access":"write","require_table_match":true}]}`},
	{"guardrail", "edge-no-drop",
		`{"rules":[{"name":"no-drop","type":"operation","operations":["drop"],"message":"metrics are append only"}]}`},
	{"guardrail", "edge-no-secrets",
		`{"rules":[{"name":"no-secrets","type":"deny_words_list","words":["password","secret"]}]}`},
	{"guardrail", "crm-no-truncate",
		`{"rules":[{"name":"no-truncate","type":"operation","operations":["truncate"]}]}`},
	{"datamasking", "pay-mask-pii",
		`{"rules":[{"name":"ssn","entities":["US_SSN"],"strategy":"partial","keep_last":4},{"name":"ssn-column","columns":["ssn"],"strategy":"hash"}]}`},
	{"datamasking", "edge-mask-email",
		`{"rules":[{"name":"email","entities":["EMAIL_ADDRESS"],"strategy":"redact"}]}`},
	{"analyzer", "pay-risky-writes",
		`{"trigger":{"operations":["update","delete"]},"high":"require_review","medium":"warn","approval_rule":"` + rehearsalApprovalRule + `","prompt":"Treat the payments schema as high risk."}`},
}

var rehearsalBindings = []rehearsalBinding{
	{"guardrail", "pay-zz-no-destructive", "payments", "appdb", 0},
	{"guardrail", "pay-aa-read-only-customers", "payments", "appdb", 1},
	{"guardrail", "pay-aa-read-only-customers", "payments", "ledger", 0},
	{"guardrail", "edge-no-drop", "edge", "metrics", 0},
	{"guardrail", "edge-no-secrets", "edge", "orders", 0},
	{"guardrail", "crm-no-truncate", "crm", "reporting replica", 0},
	{"datamasking", "pay-mask-pii", "payments", "appdb", 0},
	{"datamasking", "edge-mask-email", "edge", "api", 0},
	{"analyzer", "pay-risky-writes", "payments", "appdb", 0},
}

var rehearsalSlack = []rehearsalSlackChannels{
	{"payments", "appdb", []string{"C0PAYMENTS1", "C0DBAONCALL"}},
	{"crm", "reporting replica", []string{"C0CRMDATA"}},
}

var rehearsalReviews = []rehearsalReview{
	{ID: "5eed0000-0000-4000-8000-0000000000c1", SessionID: "5eed0000-0000-4000-8000-0000000000d1",
		Sidecar: "payments", Listener: "appdb", Rule: rehearsalApprovalRule, Status: "PENDING",
		StatementHash: rehearsalHash("1"), CreatedAt: rehearsalBase.Add(80 * time.Hour)},
	{ID: "5eed0000-0000-4000-8000-0000000000c2", SessionID: "5eed0000-0000-4000-8000-0000000000d2",
		Sidecar: "payments", Listener: "appdb", Rule: rehearsalApprovalRule, Status: "APPROVED",
		StatementHash: rehearsalHash("2"), CreatedAt: rehearsalBase.Add(81 * time.Hour)},
	{ID: "5eed0000-0000-4000-8000-0000000000c3", SessionID: "5eed0000-0000-4000-8000-0000000000d3",
		Sidecar: "payments", Listener: "appdb", Rule: rehearsalApprovalRule, Status: "REJECTED",
		StatementHash: rehearsalHash("3"), RejectionReason: "not during the freeze", CreatedAt: rehearsalBase.Add(82 * time.Hour)},
	{ID: "5eed0000-0000-4000-8000-0000000000c4", SessionID: "5eed0000-0000-4000-8000-0000000000d4",
		Sidecar: "payments", Listener: "appdb", Rule: rehearsalApprovalRule, Status: "EXECUTED",
		StatementHash: rehearsalHash("4"), CreatedAt: rehearsalBase.Add(83 * time.Hour)},
}

// rehearsalHash is a statement hash: 64 hex characters.
func rehearsalHash(seed string) string {
	return fmt.Sprintf("%064s", seed+"ab")
}

func rehearsalSidecarByName(name string) rehearsalSidecar {
	for _, s := range rehearsalSidecars {
		if s.Name == name {
			return s
		}
	}
	panic("parity: no rehearsal sidecar " + name)
}

// newRehearsalMigrate opens golang-migrate on the embedded migrations, as
// gateway/models/bootstrap does at startup.
func newRehearsalMigrate(dbURI string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrationfiles.FS, ".")
	if err != nil {
		return nil, err
	}
	return migrate.NewWithSourceInstance("iofs", src, dbURI)
}

// migrateRehearsal runs step and releases both handles.
func migrateRehearsal(dbURI string, step func(m *migrate.Migrate) error) error {
	m, err := newRehearsalMigrate(dbURI)
	if err != nil {
		return err
	}
	stepErr := step(m)
	srcErr, dbErr := m.Close()
	return errors.Join(stepErr, srcErr, dbErr)
}

// latestMigrationVersion is the newest version embedded in the binary.
func latestMigrationVersion() (uint, error) {
	src, err := iofs.New(migrationfiles.FS, ".")
	if err != nil {
		return 0, err
	}
	defer src.Close()
	v, err := src.First()
	if err != nil {
		return 0, err
	}
	for {
		next, err := src.Next(v)
		if errors.Is(err, fs.ErrNotExist) {
			return v, nil
		}
		if err != nil {
			return 0, err
		}
		v = next
	}
}

// seedControlPlaneCopy builds the copy: schema at rehearsalVersion, then the
// rows. The default org has no users, so the harness registers the admin.
func seedControlPlaneCopy(dbURI string) error {
	for _, s := range rehearsalSidecars {
		// The row is decoded strictly by the gateway (models.SidecarConfiguration);
		// a fixture typo must stop here, not read as a migration failure.
		dec := json.NewDecoder(bytes.NewReader([]byte(s.Config)))
		dec.DisallowUnknownFields()
		var cfg daemon.Config
		if err := dec.Decode(&cfg); err != nil {
			return fmt.Errorf("rehearsal sidecar %s: configuration does not decode: %w", s.Name, err)
		}
	}
	err := migrateRehearsal(dbURI, func(m *migrate.Migrate) error { return m.Migrate(rehearsalVersion) })
	if err != nil {
		return fmt.Errorf("migrating to %d: %w", rehearsalVersion, err)
	}
	db, err := sql.Open("postgres", dbURI)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	exec := func(query string, args ...any) {
		if err != nil {
			return
		}
		if _, e := tx.Exec(query, args...); e != nil {
			err = fmt.Errorf("%w\nquery: %s", e, query)
		}
	}

	exec(`INSERT INTO private.orgs (id, name, created_at) VALUES ($1, $2, $3)`,
		rehearsalOrgID, proto.DefaultOrgName, rehearsalBase.Add(-24*time.Hour))
	exec(`INSERT INTO private.org_feature_flags (org_id, name, enabled, updated_by) VALUES ($1, 'beta.sidecar_listeners', true, $2)`,
		rehearsalOrgID, rehearsalCreator)

	// The ordinary connection whose name crm's appdb mirror would take.
	exec(`INSERT INTO private.resources (org_id, name, type, subtype) VALUES ($1, $2, 'database', 'postgres')`,
		rehearsalOrgID, rehearsalCollision)
	exec(`INSERT INTO private.connections (org_id, name, resource_name, type, subtype, status,
			access_mode_runbooks, access_mode_exec, access_mode_connect, access_schema)
		VALUES ($1, $2, $2, 'database', 'postgres', 'offline', 'enabled', 'enabled', 'enabled', 'enabled')`,
		rehearsalOrgID, rehearsalCollision)

	for _, s := range rehearsalSidecars {
		exec(`INSERT INTO private.sidecars (id, org_id, name, key_hash, created_by, created_at, configuration,
				last_seen_at, reported_version, served_revision, applied_revision, last_outcome, last_error,
				served_revision_at, capabilities)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''),
				NULLIF($13, ''), $14, $15)`,
			s.ID, rehearsalOrgID, s.Name, rehearsalKeyHash(s.Token), s.CreatedBy, s.CreatedAt, s.Config,
			s.LastSeenAt, s.ReportedVersion, s.ServedRevision, s.AppliedRevision, s.LastOutcome, s.LastError,
			sql.NullTime{Time: s.LastSeenAt, Valid: s.ServedRevision != ""}, pq.StringArray(s.Capabilities))
	}

	for _, r := range rehearsalRules {
		desc := "Written in the control plane for the " + r.Name + " policy."
		switch r.Kind {
		case "guardrail":
			exec(`INSERT INTO private.guardrail_rules (org_id, name, description, input, output, sidecar_spec)
				VALUES ($1, $2, $3, '{}'::jsonb, '{}'::jsonb, $4::jsonb)`, rehearsalOrgID, r.Name, desc, r.Spec)
		case "datamasking":
			exec(`INSERT INTO private.datamasking_rules (org_id, name, description, supported_entity_types, custom_entity_types, sidecar_spec)
				VALUES ($1, $2, $3, '[]'::jsonb, '[]'::jsonb, $4::jsonb)`, rehearsalOrgID, r.Name, desc, r.Spec)
		case "analyzer":
			exec(`INSERT INTO private.ai_session_analyzer_rules (org_id, name, description, connection_names, risk_evaluation, sidecar_spec)
				VALUES ($1, $2, $3, '{}', $4::jsonb, $5::jsonb)`, rehearsalOrgID, r.Name, desc,
				`{"low_risk":{"action":"allow_execution"},"medium_risk":{"action":"allow_execution"},"high_risk":{"action":"allow_execution"}}`,
				r.Spec)
		default:
			return fmt.Errorf("rehearsal rule %s: unknown kind %q", r.Name, r.Kind)
		}
	}

	for _, b := range rehearsalBindings {
		table, column, err2 := rulesListenersTable(b.Kind)
		if err2 != nil {
			return err2
		}
		exec(`INSERT INTO private.`+table+` (org_id, `+column+`, sidecar_id, listener_name, position)
			VALUES ($1, $2, $3, $4, $5)`, rehearsalOrgID, b.Rule, rehearsalSidecarByName(b.Sidecar).ID, b.Listener, b.Position)
	}

	exec(`INSERT INTO private.access_request_rules (org_id, name, description, connection_names, access_type,
			approval_required_groups, all_groups_must_approve, reviewers_groups, force_approval_groups, min_approvals)
		VALUES ($1, $2, 'Who releases a held payments statement.', '{}', 'sidecar', '{}', false, '{admin,dba}', '{}', 1)`,
		rehearsalOrgID, rehearsalApprovalRule)

	for _, sc := range rehearsalSlack {
		exec(`INSERT INTO private.sidecar_slack_channels (org_id, sidecar_id, listener_name, channels) VALUES ($1, $2, $3, $4)`,
			rehearsalOrgID, rehearsalSidecarByName(sc.Sidecar).ID, sc.Listener, pq.StringArray(sc.Channels))
	}

	// Sidecar reviews need no user: owner_id is the sidecar, as
	// api/sidecar/reviews.go files them, and each one has its session.
	for _, r := range rehearsalReviews {
		sc := rehearsalSidecarByName(r.Sidecar)
		sessionStatus := "open"
		if r.Status == "EXECUTED" || r.Status == "REJECTED" {
			sessionStatus = "done"
		}
		exec(`INSERT INTO private.sessions (id, org_id, connection, connection_type, verb, user_id, user_name, user_email, status, created_at)
			VALUES ($1, $2, '', 'custom', 'exec', $3, $4, 'hoop@hoop.dev', $5, $6)`,
			r.SessionID, rehearsalOrgID, sc.ID, sc.Name, sessionStatus, r.CreatedAt)
		exec(`INSERT INTO private.reviews (id, org_id, session_id, connection_name, type, status, owner_id, owner_email, owner_name,
				created_at, access_request_rule_name, min_approvals, force_approval_groups, rejection_reason,
				sidecar_id, listener_name, statement_hash)
			VALUES ($1, $2, $3, '', 'onetime', $4, $5, 'hoop@hoop.dev', $6, $7, $8, 1, '{}', NULLIF($9, ''), $10, $11, $12)`,
			r.ID, rehearsalOrgID, r.SessionID, r.Status, sc.ID, sc.Name, r.CreatedAt, r.Rule, r.RejectionReason,
			sc.ID, r.Listener, r.StatementHash)
		groupStatus, reviewedAt := "PENDING", sql.NullTime{}
		switch r.Status {
		case "APPROVED", "EXECUTED":
			groupStatus, reviewedAt = "APPROVED", sql.NullTime{Time: r.CreatedAt.Add(10 * time.Minute), Valid: true}
		case "REJECTED":
			groupStatus, reviewedAt = "REJECTED", sql.NullTime{Time: r.CreatedAt.Add(10 * time.Minute), Valid: true}
		}
		// One approval releases the statement (min_approvals 1), so the dba
		// group never acted.
		exec(`INSERT INTO private.review_groups (org_id, review_id, group_name, status, owner_email, reviewed_at)
			VALUES ($1, $2, 'admin', $3, $4, $5)`,
			rehearsalOrgID, r.ID, groupStatus, sql.NullString{String: rehearsalCreator, Valid: reviewedAt.Valid}, reviewedAt)
		exec(`INSERT INTO private.review_groups (org_id, review_id, group_name, status) VALUES ($1, $2, 'dba', 'PENDING')`,
			rehearsalOrgID, r.ID)
	}
	if err != nil {
		return fmt.Errorf("seeding the control-plane copy: %w", err)
	}
	return tx.Commit()
}

// rehearsalKeyHash is the stored form of a sidecar token, as
// models.HashAPIKey writes it: SHA-256, lowercase hex.
func rehearsalKeyHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// rulesListenersTable names the junction a kind binds to a listener through,
// and its rule-name column (migration 000120).
func rulesListenersTable(kind string) (table, column string, err error) {
	switch kind {
	case "guardrail":
		return "guardrail_rules_listeners", "guardrail_rule_name", nil
	case "datamasking":
		return "datamasking_rules_listeners", "datamasking_rule_name", nil
	case "analyzer":
		return "ai_session_analyzer_rules_listeners", "analyzer_rule_name", nil
	}
	return "", "", fmt.Errorf("unknown rule kind %q", kind)
}
