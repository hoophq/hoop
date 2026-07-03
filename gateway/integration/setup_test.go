//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	apiserverconfig "github.com/hoophq/hoop/gateway/api/serverconfig"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/integration/testutil"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/services"
)

// testServer is the shared gateway server under test. It is initialized once
// in TestMain and used by every test in this package.
var testServer *testutil.GatewayTestServer

// TestMain boots a throwaway PostgreSQL container, runs the full migration
// set, bootstraps the default organization the way gateway/main.go does
// (minus plugins, proxies, and the gRPC server, none of which the smoke
// tests exercise), then serves the gateway's complete gin route tree via an
// in-process httptest server.
func TestMain(m *testing.M) {
	code, err := runMain(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gateway smoke setup failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runMain(m *testing.M) (int, error) {
	ctx := context.Background()

	pg, err := testutil.StartPostgres(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		if termErr := pg.Terminate(); termErr != nil {
			fmt.Fprintf(os.Stderr, "gateway smoke teardown: failed terminating postgres container: %v\n", termErr)
		}
	}()

	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "rootfs", "app", "migrations"))
	if err != nil {
		return 0, fmt.Errorf("resolving migrations path: %w", err)
	}

	// appconfig.Load reads these env vars and stat-checks the migration path,
	// so they must be set before Load. AUTH_METHOD=local makes the gateway
	// issue/verify its own ed25519-signed JWTs without an external IDP.
	for k, v := range map[string]string{
		"POSTGRES_DB_URI":      pg.URI(),
		"MIGRATION_PATH_FILES": migrationsPath,
		"AUTH_METHOD":          "local",
		"API_URL":              "http://127.0.0.1:8009",
		"GRPC_URL":             "grpc://127.0.0.1:8010",
		"GIN_MODE":             "release",
	} {
		if err := os.Setenv(k, v); err != nil {
			return 0, fmt.Errorf("setenv %s: %w", k, err)
		}
	}

	if err := appconfig.Load(); err != nil {
		return 0, fmt.Errorf("appconfig.Load: %w", err)
	}

	if err := modelsbootstrap.MigrateDB(appconfig.Get().PgURI(), appconfig.Get().MigrationPathFiles()); err != nil {
		return 0, fmt.Errorf("migrate db: %w", err)
	}
	if err := models.InitDatabaseConnection(); err != nil {
		return 0, fmt.Errorf("init db connection: %w", err)
	}
	if err := modelsbootstrap.RunGolangMigrations(); err != nil {
		return 0, fmt.Errorf("run golang migrations: %w", err)
	}

	// Warm the same caches gateway/main.go warms after migrations so the
	// feature-flag and analytics-mode codepaths behave as they do in
	// production rather than against cold caches.
	services.WarmFeatureFlagCache()
	analytics.WarmModeCache()

	if err := bootstrapDefaultOrg(); err != nil {
		return 0, fmt.Errorf("bootstrap default org: %w", err)
	}

	handler := buildTestHandler()
	testServer = testutil.NewGatewayTestServer(handler)
	defer testServer.Close()

	return m.Run(), nil
}

// bootstrapDefaultOrg mirrors the single-tenant org bootstrap in
// gateway/main.go (lines ~109-157), limited to what the API needs to accept
// authenticated requests: the default org, its connection tags, the local
// signing key (created lazily by the token verifier provider), and the global
// user-role configuration consumed by the RBAC middleware.
//
// It intentionally omits the default runbook configuration, rulepack seeding,
// and org agent-key provisioning: none of the smoke tests exercise those
// features. If a future test does, add the corresponding bootstrap step here
// (and mirror it from gateway/main.go) rather than relying on it being absent.
func bootstrapDefaultOrg() error {
	// Instantiating the token verifier provider generates and persists the
	// shared ed25519 signing key for the local provider on first call.
	if _, _, err := idp.NewTokenVerifierProvider(); err != nil {
		return fmt.Errorf("init token verifier provider: %w", err)
	}

	org, _, err := models.CreateOrgGetOrganization(proto.DefaultOrgName, nil)
	if err != nil {
		return fmt.Errorf("create default org: %w", err)
	}

	if err := models.UpsertBatchConnectionTags(apiconnections.DefaultConnectionTags(org.ID)); err != nil {
		return fmt.Errorf("seed default connection tags: %w", err)
	}

	// The RBAC middleware reads the global gateway user roles; without this
	// the admin/read-only role checks have no role definitions to match.
	if err := apiserverconfig.SetGlobalGatewayUserRoles(); err != nil {
		return fmt.Errorf("set global gateway user roles: %w", err)
	}

	return nil
}

// buildTestHandler constructs the exact gin engine StartAPI serves, via the
// shared api.BuildEngine constructor — same middleware chain (security, CORS,
// Sentry), same /api route tree, and the same request validators. Tests
// therefore exercise the production HTTP stack rather than a stripped-down
// router. The ReleaseConnectionFn is a no-op because the review-approval
// transport path is never exercised by the smoke tests.
func buildTestHandler() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	a := &api.Api{
		ReleaseConnectionFn: func(_, _, _, _ string) {},
	}
	return a.BuildEngine()
}
