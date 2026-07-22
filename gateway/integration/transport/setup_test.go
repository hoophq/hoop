//go:build integration

package transport

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/agent/controller"
	"github.com/hoophq/hoop/common/clientconfig"
	"github.com/hoophq/hoop/common/dsnkeys"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/integration/testutil"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// gw is the shared gateway under test, booted once in TestMain with the full
// transport stack (plugins + gRPC) plus the HTTP API used for faithful,
// API-driven bootstrap of users, agents and connections.
var gw *testutil.Gateway

// TestMain boots the gateway once for the whole package. The transport suite
// needs three layers: the HTTP API (to register the admin user and create
// connections the way an operator would), the plugin chain (client sessions
// flow through PluginExecOnReceive), and the gRPC transport server (the system
// under test).
func TestMain(m *testing.M) {
	code, err := runMain(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "transport harness setup failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runMain(m *testing.M) (int, error) {
	g, err := testutil.StartGateway(context.Background(), testutil.GatewayOptions{
		WithHTTP:    true,
		WithPlugins: true,
		WithGRPC:    true,
	})
	if err != nil {
		return 0, err
	}
	defer g.Close()
	gw = g
	return m.Run(), nil
}

// clearOrgGuardrails removes the org's guardrail rules and their associations.
//
// The default rulepacks migration seeds per-org "Security Pack" guardrails that
// auto-attach to database connections by attribute (e.g. the PostgreSQL
// Security Pack), and connection creation re-materializes that association. Since
// #1573 the gateway fail-closes any guarded session when no Presidio provider is
// configured — and this suite runs no Presidio (guardrail enforcement is
// agent-side middleware, orthogonal to transport parity). Callers invoke this
// after creating a connection so the session under test exercises the wire, not
// guardrail admission. Guardrail behavior is covered by the smoke suite and unit
// tests, not here.
func clearOrgGuardrails(t *testing.T) {
	t.Helper()
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM private.guardrail_rules_connections
			WHERE rule_id IN (SELECT id FROM private.guardrail_rules WHERE org_id = ?)`, gw.OrgID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM private.guardrail_rules_attributes WHERE org_id = ?`, gw.OrgID).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM private.guardrail_rules WHERE org_id = ?`, gw.OrgID).Error
	})
	if err != nil {
		t.Fatalf("clearOrgGuardrails: %v", err)
	}
}

// --- shared identities ---------------------------------------------------

var (
	adminOnce sync.Once
	adminTok  string
)

// adminToken registers the default org's first (admin) user on first call and
// returns its JWT, reused across the suite. Registration can only happen once,
// so it is guarded by a sync.Once.
func adminToken(t *testing.T) string {
	t.Helper()
	adminOnce.Do(func() { adminTok = testutil.RegisterFirstUser(t, gw.HTTP) })
	return adminTok
}

// --- bootstrap helpers ---------------------------------------------------

// uniqueName returns a collision-free identifier for per-test resources.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, uuid.NewString()[:8])
}

// createAgent registers a standard-mode agent directly in the model layer and
// returns its id plus a DSN token pointing at the harness gRPC address. The
// DSN is what the agent presents to the auth interceptor.
func createAgent(t *testing.T, name string) (agentID, dsn string) {
	t.Helper()
	secret := "itest-secret-" + name
	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(secret)))
	if err := models.CreateAgent(gw.OrgID, name, pb.AgentModeStandardType, keyHash); err != nil {
		t.Fatalf("createAgent: %v", err)
	}
	ag, err := models.GetAgentByNameOrID(gw.OrgID, name)
	if err != nil {
		t.Fatalf("createAgent: lookup %q: %v", name, err)
	}
	dsn, err = dsnkeys.NewString("grpc://"+gw.GRPCAddr, name, secret, pb.AgentModeStandardType)
	if err != nil {
		t.Fatalf("createAgent: build dsn: %v", err)
	}
	return ag.ID, dsn
}

// postPGConnection creates a postgres connection via the HTTP API (the faithful
// operator path), pointed at the harness Postgres container so the agent proxies
// real traffic to a real database. It performs no guardrail cleanup, so the
// connection keeps the default security-pack guardrails the gateway attaches.
func postPGConnection(t *testing.T, name, agentID string) {
	t.Helper()
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	body := openapi.Connection{
		Name:    name,
		Type:    "database",
		SubType: "postgres",
		AgentId: agentID,
		Command: []string{},
		Secrets: map[string]any{
			"envvar:HOST":    b64(gw.Postgres.Host),
			"envvar:PORT":    b64(gw.Postgres.Port),
			"envvar:USER":    b64(gw.Postgres.User),
			"envvar:PASS":    b64(gw.Postgres.Password),
			"envvar:DB":      b64(gw.Postgres.Database),
			"envvar:SSLMODE": b64("disable"),
		},
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "disabled",
	}
	resp := gw.HTTP.Post(t, "/connections", adminToken(t), body)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, 201)
}

// createPGConnection creates a postgres connection and strips the default
// security-pack guardrails the gateway auto-attaches, so a session on it is not
// fail-closed on a missing Presidio provider (see clearOrgGuardrails). Use this
// for tests that need a session to actually open.
func createPGConnection(t *testing.T, name, agentID string) {
	t.Helper()
	postPGConnection(t, name, agentID)
	clearOrgGuardrails(t)
}

// connectionID looks up a connection's UUID via the API.
func connectionID(t *testing.T, name string) string {
	t.Helper()
	resp := gw.HTTP.Get(t, "/connections/"+name, adminToken(t))
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, 200)
	var conn openapi.Connection
	testutil.DecodeJSON(t, resp, &conn)
	if conn.ID == "" {
		t.Fatalf("connectionID: %q has no id", name)
	}
	return conn.ID
}

// createGuardrailForConnection creates a guardrail rule bound to the given
// connection via the API. Used to make a connection deterministically guarded
// regardless of the shared org's default-rulepack state.
func createGuardrailForConnection(t *testing.T, name, connectionID string) {
	t.Helper()
	body := openapi.GuardRailRuleRequest{
		Name: name,
		Input: map[string]any{
			"rules": []map[string]any{
				{"type": "deny_words_list", "words": []string{"SELECT"}, "pattern_regex": ""},
			},
		},
		Output:        map[string]any{"rules": []map[string]any{}},
		ConnectionIDs: []string{connectionID},
	}
	resp := gw.HTTP.Post(t, "/guardrails", adminToken(t), body)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, 201)
}

// createOutputGuardrailForConnection creates a guardrail rule with OUTPUT rules
// bound to the given connection, used to exercise direction-aware admission.
func createOutputGuardrailForConnection(t *testing.T, name, connectionID string) {
	t.Helper()
	body := openapi.GuardRailRuleRequest{
		Name:  name,
		Input: map[string]any{"rules": []map[string]any{}},
		Output: map[string]any{
			"rules": []map[string]any{
				{"type": "deny_words_list", "words": []string{"secret"}, "pattern_regex": ""},
			},
		},
		ConnectionIDs: []string{connectionID},
	}
	resp := gw.HTTP.Post(t, "/guardrails", adminToken(t), body)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, 201)
}

// postMSSQLConnection creates an mssql connection via the HTTP API. The upstream
// host need not be reachable: these tests exercise gateway-side admission, which
// happens at session-open before any protocol proxy connects to the database.
func postMSSQLConnection(t *testing.T, name, agentID string) {
	t.Helper()
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	body := openapi.Connection{
		Name:    name,
		Type:    "database",
		SubType: "mssql",
		AgentId: agentID,
		Command: []string{},
		Secrets: map[string]any{
			"envvar:HOST": b64("127.0.0.1"),
			"envvar:PORT": b64("1433"),
			"envvar:USER": b64("sa"),
			"envvar:PASS": b64("unused-for-admission"),
		},
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "disabled",
	}
	resp := gw.HTTP.Post(t, "/connections", adminToken(t), body)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, 201)
}

// startAgent dials the gateway as the given agent and runs a real agent
// controller against the stream, exactly as the production agent does. It
// registers a t.Cleanup that tears the controller (and its stream) down.
func startAgent(t *testing.T, c Connector, dsn string) {
	t.Helper()
	client, err := c.DialAgent(context.Background(), dsn)
	if err != nil {
		t.Fatalf("startAgent: dial: %v", err)
	}
	runAgentController(t, dsn, client)
}

// startAgentWithoutCapabilities runs an agent whose stream advertises no
// capabilities, standing in for an agent older than capability advertisement.
func startAgentWithoutCapabilities(t *testing.T, c Connector, dsn string) {
	t.Helper()
	gc, ok := c.(*grpcConnector)
	if !ok {
		t.Skipf("startAgentWithoutCapabilities requires the grpc connector, got %T", c)
	}
	client, err := gc.dialAgentWithoutCapabilities(dsn)
	if err != nil {
		t.Fatalf("startAgentWithoutCapabilities: dial: %v", err)
	}
	runAgentController(t, dsn, client)
}

func runAgentController(t *testing.T, dsn string, client pb.ClientTransport) {
	t.Helper()
	cfg := &config.Config{
		Name:      "itest-agent",
		Type:      clientconfig.ModeDsn,
		AgentMode: pb.AgentModeStandardType,
		Token:     dsn,
		URL:       gw.GRPCAddr,
	}
	ctrl := controller.New(client, cfg, nil)
	go func() { _ = ctrl.Run() }()
	t.Cleanup(func() { ctrl.Close(nil) })
}

// waitConnectionStatus polls the connections API until the named connection
// reaches the wanted status ("online"/"offline"). It gives the coordination
// between agent connect/disconnect and the gateway's status bookkeeping a
// deterministic gate.
func waitConnectionStatus(t *testing.T, name, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		resp := gw.HTTP.Get(t, "/connections/"+name, adminToken(t))
		var conn openapi.Connection
		testutil.DecodeJSON(t, resp, &conn)
		resp.Body.Close()
		last = conn.Status
		if conn.Status == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("waitConnectionStatus: %q never became %q (last=%q)", name, want, last)
}

// waitConnectionOnline is the common case of waitConnectionStatus.
func waitConnectionOnline(t *testing.T, name string) {
	t.Helper()
	waitConnectionStatus(t, name, "online")
}
