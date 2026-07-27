//go:build integration

// Package standalone is the integration suite for the single-binary
// standalone mode (DEP-38): the gateway running on the embedded PGlite
// database with the dedicated `standalone` agent connected in-process.
//
// The suite boots the same layers `hoop start standalone` boots — embedded
// database, HTTP API, plugin chain, gRPC transport — and drives the exact
// provisioning code path the command runs (services.StandaloneAgentDSN). It
// exists so a change that breaks the single binary fails here, in CI,
// rather than on a user's first `hoop start standalone`.
package standalone

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hoophq/hoop/gateway/integration/testutil"
)

// gw is the shared gateway under test, booted once in TestMain on the
// embedded PGlite backend with the full transport stack.
var gw *testutil.Gateway

func TestMain(m *testing.M) {
	code, err := runMain(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "standalone harness setup failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runMain(m *testing.M) (int, error) {
	g, err := testutil.StartGateway(context.Background(), testutil.GatewayOptions{
		WithHTTP:    true,
		WithPlugins: true,
		WithGRPC:    true,
		Database:    testutil.DBPglite,
	})
	if err != nil {
		return 0, err
	}
	defer g.Close()
	gw = g
	return m.Run(), nil
}
