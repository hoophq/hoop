//go:build integration

// Package controlplane is the integration suite for the routes only a control
// plane serves: the sidecar endpoints a fleet of sidecars calls.
//
// It is a separate test binary from gateway/integration because appconfig.Load
// is one-shot. A process runs as a gateway or as a control plane for its whole
// life, and POST /sidecars/reviews answers 412 in the other mode.
//
// The suite exists for behaviour that only a database can show: a partial
// unique index deciding who files, and a conditional UPDATE deciding who
// consumes an approval. Neither is reachable from a unit test.
package controlplane

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/integration/testutil"
)

// gw is the shared control plane under test, booted once in TestMain.
var gw *testutil.Gateway

func TestMain(m *testing.M) {
	code, err := runMain(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "control plane harness setup failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runMain(m *testing.M) (int, error) {
	// HTTP only: a control plane serves the API and registers no agents, so
	// there is no plugin chain and no gRPC transport to start.
	opts := testutil.GatewayOptions{
		WithHTTP: true,
		AppMode:  appconfig.AppModeControlPlane,
	}
	if db := os.Getenv("GATEWAY_TEST_DB"); db != "" {
		opts.Database = testutil.DatabaseBackend(db)
	}

	g, err := testutil.StartGateway(context.Background(), opts)
	if err != nil {
		return 0, err
	}
	defer g.Close()
	gw = g
	return m.Run(), nil
}
