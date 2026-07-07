//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hoophq/hoop/gateway/integration/testutil"
)

// testServer is the shared gateway HTTP server under test. It is initialized
// once in TestMain and used by every test in this package.
var testServer *testutil.GatewayTestServer

// testGateway is the fully booted gateway backing testServer, exposing the
// state-store backend to tests that need direct database access.
var testGateway *testutil.Gateway

// TestMain boots a throwaway state store (a PostgreSQL container by default,
// or the embedded PGlite database when GATEWAY_TEST_DB=pglite), runs the full
// migration set, bootstraps the default organization the way gateway/main.go
// does (minus plugins, proxies, and the gRPC server, none of which the smoke
// tests exercise), then serves the gateway's complete gin route tree via an
// in-process httptest server. The shared boot lives in testutil.StartGateway
// so this suite and the transport suite share one bootstrap implementation.
//
// Running the identical suite against both backends is deliberate: the
// pglite pass is the standalone-mode regression net — it fails on SQL the
// embedded backend cannot run and on code that deadlocks a pool capped at
// one connection, both invisible on regular PostgreSQL.
func TestMain(m *testing.M) {
	code, err := runMain(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gateway smoke setup failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runMain(m *testing.M) (int, error) {
	// Data-masking rule mutations are gated on a configured DLP provider
	// (services.CheckRedactProvider). The gate only checks configuration —
	// rule CRUD never contacts the provider — so pointing the config at
	// unreachable URLs lets the suite exercise the full masking CRUD
	// surface. The 422-without-provider contract is covered by unit tests
	// in gateway/api/datamasking.
	for k, v := range map[string]string{
		"DLP_PROVIDER":              "mspresidio",
		"MSPRESIDIO_ANALYZER_URL":   "http://127.0.0.1:1",
		"MSPRESIDIO_ANONYMIZER_URL": "http://127.0.0.1:1",
	} {
		if err := os.Setenv(k, v); err != nil {
			return 0, fmt.Errorf("setenv %s: %w", k, err)
		}
	}

	opts := testutil.GatewayOptions{WithHTTP: true}
	if db := os.Getenv("GATEWAY_TEST_DB"); db != "" {
		opts.Database = testutil.DatabaseBackend(db)
	}
	gw, err := testutil.StartGateway(context.Background(), opts)
	if err != nil {
		return 0, err
	}
	defer gw.Close()

	testServer = gw.HTTP
	testGateway = gw
	return m.Run(), nil
}
