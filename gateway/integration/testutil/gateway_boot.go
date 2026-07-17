//go:build integration

package testutil

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	apiserverconfig "github.com/hoophq/hoop/gateway/api/serverconfig"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/transport"
	pluginsrbac "github.com/hoophq/hoop/gateway/transport/plugins/accesscontrol"
	pluginsaudit "github.com/hoophq/hoop/gateway/transport/plugins/audit"
	pluginsdlp "github.com/hoophq/hoop/gateway/transport/plugins/dlp"
	pluginsreview "github.com/hoophq/hoop/gateway/transport/plugins/review"
	pluginsslack "github.com/hoophq/hoop/gateway/transport/plugins/slack"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	pluginswebhooks "github.com/hoophq/hoop/gateway/transport/plugins/webhooks"
)

// DatabaseBackend selects the state store StartGateway boots the gateway
// against.
type DatabaseBackend string

const (
	// DBPostgresContainer runs a throwaway PostgreSQL testcontainer (the
	// default, zero-value backend).
	DBPostgresContainer DatabaseBackend = ""
	// DBPglite runs the embedded PGlite (wasm) database inside the test
	// process, exactly as `POSTGRES_DB_URI=pglite://` deployments do — pool
	// capped at one connection, embedded migration set, no Docker. Suites
	// run against this backend to catch regressions that only manifest on
	// the single-session backend (pool=1 deadlocks, unsupported SQL).
	DBPglite DatabaseBackend = "pglite"
)

// GatewayOptions selects which layers of the gateway a test suite boots. The
// smoke suite only needs the HTTP API; the transport suite additionally needs
// the plugin chain and the gRPC transport server. Keeping the layers opt-in
// means each suite pays only for what it exercises and the two share one
// bootstrap implementation.
type GatewayOptions struct {
	// WithHTTP starts the full gin route tree behind an httptest server,
	// reachable via Gateway.HTTP.
	WithHTTP bool
	// WithPlugins registers the production transport plugin chain (review,
	// audit, dlp, accesscontrol, webhooks, slack) in the same order as
	// gateway/main.go and runs each plugin's OnStartup. Required for any
	// client session that flows through PluginExecOnReceive.
	WithPlugins bool
	// WithGRPC starts the transport gRPC server on an ephemeral loopback
	// port, reachable via Gateway.GRPCAddr.
	WithGRPC bool
	// Database selects the state store backend; defaults to
	// DBPostgresContainer.
	Database DatabaseBackend
}

// Gateway is a fully booted, in-process gateway under test. It owns every
// backing resource (state-store backend, HTTP server, gRPC server) and tears
// them all down on Close.
type Gateway struct {
	// Postgres is the throwaway state-store container; nil when the suite
	// runs on the DBPglite backend.
	Postgres *PostgresContainer
	// Pglite is the embedded database instance; nil unless the suite runs
	// on the DBPglite backend.
	Pglite *pglite.Instance
	// HTTP is the in-process HTTP API server; nil unless WithHTTP was set.
	HTTP *GatewayTestServer
	// GRPCAddr is the "host:port" of the transport gRPC server; empty unless
	// WithGRPC was set.
	GRPCAddr string
	// OrgID is the id of the bootstrapped default organization.
	OrgID string

	grpcServer    *grpc.Server
	grpcListener  net.Listener
	auditPath     string
	pgliteDataDir string
}

// StartGateway boots a gateway in-process the way gateway/main.go does, minus
// the layers a test does not opt into. The boot order mirrors production:
// load config, run migrations, warm caches, bootstrap the default org, then
// (optionally) register plugins and start the HTTP and gRPC servers.
//
// PROCESS-EXCLUSIVE: the gateway is singleton-heavy — this function mutates
// process globals (os.Setenv, the appconfig singleton, the global models.DB and
// its warmed caches, plugintypes.AuditPath and plugintypes.RegisteredPlugins).
// Call it exactly once per test binary, from TestMain, and never concurrently.
// Two live gateways in one process would clobber each other's config, DB
// handle, and plugin registry. This matches how gateway/main.go treats the same
// globals in production (one gateway per process).
//
// The returned Gateway must be closed by the caller (typically via defer in
// TestMain) to release the container and network listeners.
func StartGateway(ctx context.Context, opts GatewayOptions) (gw *Gateway, err error) {
	gw = &Gateway{}
	// On any failure past this point, tear every already-started resource
	// back down so a bootstrap error does not leak containers, embedded
	// databases, or listeners for the whole run.
	defer func() {
		if err != nil {
			gw.Close()
			gw = nil
		}
	}()

	// appconfig.Load reads these env vars, so they must be set before Load.
	// AUTH_METHOD=local makes the gateway issue/verify its own
	// ed25519-signed JWTs without an external IDP.
	env := map[string]string{
		"AUTH_METHOD": "local",
		"API_URL":     "http://127.0.0.1:8009",
		"GRPC_URL":    "grpc://127.0.0.1:8010",
		"GIN_MODE":    "release",
	}

	switch opts.Database {
	case DBPglite:
		// The embedded database boots from a throwaway data directory; the
		// pglite:// URI only carries the directory (gateway/main.go boots
		// the instance after appconfig.Load, and so does this harness).
		dataDir, derr := os.MkdirTemp("", "hoop-pglite-*")
		if derr != nil {
			return nil, fmt.Errorf("create pglite data dir: %w", derr)
		}
		gw.pgliteDataDir = dataDir
		env["POSTGRES_DB_URI"] = "pglite://" + dataDir
	case DBPostgresContainer:
		pg, perr := StartPostgres(ctx)
		if perr != nil {
			return nil, perr
		}
		gw.Postgres = pg
		env["POSTGRES_DB_URI"] = pg.URI()
	default:
		return nil, fmt.Errorf("unknown database backend %q", opts.Database)
	}

	for k, v := range env {
		if serr := os.Setenv(k, v); serr != nil {
			return nil, fmt.Errorf("setenv %s: %w", k, serr)
		}
	}

	if err = appconfig.Load(); err != nil {
		return nil, fmt.Errorf("appconfig.Load: %w", err)
	}

	// Mirror the database boot in gateway/main.go: on pglite the instance
	// is started in-process and the pool is capped at one connection (the
	// embedded backend serves a single wire-protocol session at a time).
	pgURI := appconfig.Get().PgURI()
	migrateURI, dbMaxOpenConns := pgURI, 0
	if opts.Database == DBPglite {
		inst, perr := pglite.Start(ctx, appconfig.Get().PgliteDataDir())
		if perr != nil {
			return nil, fmt.Errorf("start embedded pglite: %w", perr)
		}
		gw.Pglite = inst
		pgURI, migrateURI, dbMaxOpenConns = inst.DSN(), inst.MigrateDSN(), 1
	}

	if err = modelsbootstrap.MigrateDB(migrateURI, appconfig.Get().MigrationPathFiles()); err != nil {
		return nil, fmt.Errorf("migrate db: %w", err)
	}
	if err = models.InitDatabaseConnection(pgURI, dbMaxOpenConns); err != nil {
		return nil, fmt.Errorf("init db connection: %w", err)
	}
	if err = modelsbootstrap.RunGolangMigrations(); err != nil {
		return nil, fmt.Errorf("run golang migrations: %w", err)
	}

	// Warm the same caches gateway/main.go warms after migrations so the
	// feature-flag and analytics-mode codepaths behave as they do in
	// production rather than against cold caches.
	services.WarmFeatureFlagCache()
	analytics.WarmModeCache()

	orgID, err := bootstrapDefaultOrg()
	if err != nil {
		return nil, fmt.Errorf("bootstrap default org: %w", err)
	}

	gw.OrgID = orgID

	if opts.WithPlugins {
		if err = gw.registerPlugins(); err != nil {
			return nil, fmt.Errorf("register plugins: %w", err)
		}
	}

	if opts.WithHTTP {
		gw.HTTP = NewGatewayTestServer(buildEngine())
	}

	if opts.WithGRPC {
		if err = gw.startGRPC(); err != nil {
			return nil, fmt.Errorf("start grpc transport: %w", err)
		}
	}

	return gw, nil
}

// Close tears down every resource the gateway owns. Safe to call once.
func (g *Gateway) Close() {
	if g == nil {
		return
	}
	if g.grpcServer != nil {
		// GracefulStop stops accepting and closes the listener; close it
		// explicitly too so teardown is deterministic even if Serve never ran.
		g.grpcServer.GracefulStop()
	}
	if g.grpcListener != nil {
		_ = g.grpcListener.Close()
	}
	if g.HTTP != nil {
		g.HTTP.Close()
	}
	if g.Postgres != nil {
		if err := g.Postgres.Terminate(); err != nil {
			fmt.Fprintf(os.Stderr, "gateway test teardown: terminate postgres: %v\n", err)
		}
	}
	if g.Pglite != nil {
		// Clean shutdown checkpoints the embedded cluster, mirroring the
		// gateway's own shutdown path.
		if err := g.Pglite.Close(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "gateway test teardown: close pglite: %v\n", err)
		}
	}
	if g.pgliteDataDir != "" {
		_ = os.RemoveAll(g.pgliteDataDir)
	}
	if g.auditPath != "" {
		_ = os.RemoveAll(g.auditPath)
	}
}

// registerPlugins mirrors the plugin registration in gateway/main.go. The
// order is intentional and load-bearing (review runs before audit, etc.); do
// not reorder. The audit plugin persists session WAL logs under a path it
// stat-checks at startup, so we point it at a throwaway temp dir first.
func (g *Gateway) registerPlugins() error {
	auditPath, err := os.MkdirTemp("", "hoop-audit-*")
	if err != nil {
		return fmt.Errorf("create audit temp dir: %w", err)
	}
	g.auditPath = auditPath
	// plugintypes.AuditPath is resolved from PLUGIN_AUDIT_PATH at package
	// init, which has already run by the time TestMain executes. Assign the
	// exported var directly so the audit plugin writes WAL logs into the
	// throwaway dir instead of the production default (/opt/hoop/sessions).
	plugintypes.AuditPath = auditPath

	apiURL := appconfig.Get().ApiURL()
	noopRelease := func(_, _, _, _, _, _ string) {}
	plugintypes.RegisteredPlugins = []plugintypes.Plugin{
		pluginsreview.New(apiURL),
		pluginsaudit.New(),
		pluginsdlp.New(),
		pluginsrbac.New(),
		pluginswebhooks.New(),
		pluginsslack.New(noopRelease),
	}
	for _, p := range plugintypes.RegisteredPlugins {
		if err := p.OnStartup(plugintypes.Context{}); err != nil {
			return fmt.Errorf("plugin %s startup: %w", p.Name(), err)
		}
	}
	return nil
}

// startGRPC binds an ephemeral loopback port and serves the transport gRPC
// server built by the production wiring (transport.Server.NewGRPCServer), so
// the harness exercises the exact interceptor chain and message-size limits
// the gateway uses in production.
func (g *Gateway) startGRPC() error {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	srv := &transport.Server{AppConfig: appconfig.Get()}
	g.grpcServer = srv.NewGRPCServer()
	g.grpcListener = lis
	g.GRPCAddr = lis.Addr().String()
	go func() {
		// Serve returns ErrServerStopped after GracefulStop; that is the
		// normal teardown path and not a test failure.
		_ = g.grpcServer.Serve(lis)
	}()
	return nil
}

// bootstrapDefaultOrg mirrors the single-tenant org bootstrap in
// gateway/main.go, limited to what the API and transport need to accept
// authenticated requests: the default org, its connection tags, the local
// signing key (created lazily by the token verifier provider), and the global
// user-role configuration consumed by the RBAC middleware. It returns the
// bootstrapped organization id.
func bootstrapDefaultOrg() (string, error) {
	// Instantiating the token verifier provider generates and persists the
	// shared ed25519 signing key for the local provider on first call.
	if _, _, err := idp.NewTokenVerifierProvider(); err != nil {
		return "", fmt.Errorf("init token verifier provider: %w", err)
	}

	org, _, err := models.CreateOrgGetOrganization(proto.DefaultOrgName, nil)
	if err != nil {
		return "", fmt.Errorf("create default org: %w", err)
	}

	if err := models.UpsertBatchConnectionTags(apiconnections.DefaultConnectionTags(org.ID)); err != nil {
		return "", fmt.Errorf("seed default connection tags: %w", err)
	}

	// The RBAC middleware reads the global gateway user roles; without this
	// the admin/read-only role checks have no role definitions to match.
	if err := apiserverconfig.SetGlobalGatewayUserRoles(); err != nil {
		return "", fmt.Errorf("set global gateway user roles: %w", err)
	}

	return org.ID, nil
}

// buildEngine constructs the exact gin engine StartAPI serves, via the shared
// api.BuildEngine constructor — same middleware chain (security, CORS,
// Sentry), same /api route tree, and the same request validators. Tests
// therefore exercise the production HTTP stack rather than a stripped-down
// router. ReleaseConnectionFn is a no-op because the review-approval transport
// path is not exercised through the HTTP server.
func buildEngine() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	a := &api.Api{
		ReleaseConnectionFn: func(_, _, _, _, _, _ string) {},
	}
	return a.BuildEngine()
}
