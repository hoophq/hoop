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

// TestMain boots a throwaway PostgreSQL container, runs the full migration
// set, bootstraps the default organization the way gateway/main.go does
// (minus plugins, proxies, and the gRPC server, none of which the smoke
// tests exercise), then serves the gateway's complete gin route tree via an
// in-process httptest server. The shared boot lives in testutil.StartGateway
// so this suite and the transport suite share one bootstrap implementation.
func TestMain(m *testing.M) {
	code, err := runMain(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gateway smoke setup failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runMain(m *testing.M) (int, error) {
	gw, err := testutil.StartGateway(context.Background(), testutil.GatewayOptions{WithHTTP: true})
	if err != nil {
		return 0, err
	}
	defer gw.Close()

	testServer = gw.HTTP
	return m.Run(), nil
}
