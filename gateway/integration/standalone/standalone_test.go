//go:build integration

package standalone

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/agent/controller"
	"github.com/hoophq/hoop/common/clientconfig"
	commongrpc "github.com/hoophq/hoop/common/grpc"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/integration/testutil"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
)

// TestStandaloneLifecycle drives the standalone agent provisioning the way
// `hoop start standalone` does, against a gateway running on the embedded
// PGlite database. The subtests run in order and share the gateway state
// deliberately — each stage is a precondition of the next, mirroring the
// command's own boot sequence.
//
// DO NOT add t.Parallel() to this test or its subtests: the ordering is the
// contract under test (a non-recoverable record must be rejected BEFORE the
// first provisioning creates the real one, and reprovisioning must observe
// the record the first call created).
func TestStandaloneLifecycle(t *testing.T) {
	adminToken := testutil.RegisterFirstUser(t, gw.HTTP)
	grpcURL := "grpc://" + gw.GRPCAddr

	// A pre-existing agent named "standalone" whose key is NOT recoverable
	// (API-created agents store only the hash) must refuse provisioning
	// with remediation instructions instead of silently rotating the key.
	t.Run("refuses existing agent without recoverable key", func(t *testing.T) {
		agentID := createAgent(t, adminToken, services.StandaloneAgentName)
		defer deleteAgent(t, adminToken, agentID)

		_, err := services.StandaloneAgentDSN(grpcURL)
		if err == nil {
			t.Fatal("expected provisioning to fail against a non-recoverable agent record")
		}
		// Pin the failure to the intended contract (the remediation error),
		// not to an incidental failure elsewhere in the provisioning path.
		if !strings.Contains(err.Error(), "without a recoverable key") {
			t.Fatalf("expected the non-recoverable-key remediation error, got: %v", err)
		}
	})

	// First boot: the agent record does not exist, so provisioning creates
	// it with a recoverable key and returns a usable DSN.
	dsn, err := services.StandaloneAgentDSN(grpcURL)
	if err != nil {
		t.Fatalf("first provisioning: %v", err)
	}

	// Reboot equivalence: a second provisioning call (what every subsequent
	// `hoop start standalone` boot performs) must reconstruct the exact
	// same DSN from the stored recoverable key — no rotation, no drift.
	t.Run("reprovisioning is credential-stable", func(t *testing.T) {
		again, err := services.StandaloneAgentDSN(grpcURL)
		if err != nil {
			t.Fatalf("second provisioning: %v", err)
		}
		if again != dsn {
			t.Fatalf("provisioning is not credential-stable across boots:\nfirst:  %s\nsecond: %s", dsn, again)
		}
	})

	// The DSN must authenticate a real agent against the gateway transport:
	// run the production agent controller over it and wait until the
	// gateway's own API reports the standalone agent CONNECTED.
	t.Run("agent connects with provisioned DSN", func(t *testing.T) {
		client, err := commongrpc.Connect(commongrpc.ClientConfig{
			ServerAddress: gw.GRPCAddr,
			Token:         dsn,
			UserAgent:     "hoop-standalone-itest",
			Insecure:      true,
		}, commongrpc.WithOption("origin", pb.ConnectionOriginAgent))
		if err != nil {
			t.Fatalf("agent dial: %v", err)
		}
		ctrl := controller.New(client, &config.Config{
			Name:      services.StandaloneAgentName,
			Type:      clientconfig.ModeDsn,
			AgentMode: pb.AgentModeStandardType,
			Token:     dsn,
			URL:       gw.GRPCAddr,
		}, nil)
		go func() { _ = ctrl.Run() }()
		t.Cleanup(func() { ctrl.Close(nil) })

		waitAgentConnected(t, adminToken, services.StandaloneAgentName, 30*time.Second)
	})
}

// createAgent creates an agent via the HTTP API (hashed key only — not
// recoverable) and returns its id from the agent listing.
func createAgent(t *testing.T, token, name string) string {
	t.Helper()
	resp := gw.HTTP.Post(t, "/agents", token, openapi.AgentRequest{Name: name, Mode: "standard"})
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusCreated)
	for _, ag := range listAgents(t, token) {
		if ag.Name == name {
			return ag.ID
		}
	}
	t.Fatalf("agent %q not found in listing after create", name)
	return ""
}

func deleteAgent(t *testing.T, token, id string) {
	t.Helper()
	resp := gw.HTTP.Delete(t, "/agents/"+id, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete agent %s: unexpected status %d", id, resp.StatusCode)
	}
}

func listAgents(t *testing.T, token string) []openapi.AgentResponse {
	t.Helper()
	resp := gw.HTTP.Get(t, "/agents", token)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusOK)
	var agents []openapi.AgentResponse
	testutil.DecodeJSON(t, resp, &agents)
	return agents
}

// waitAgentConnected polls the agent listing until the named agent reports
// CONNECTED — the same signal the webapp shows an operator — or fails after
// the timeout.
func waitAgentConnected(t *testing.T, token, name string, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		for _, ag := range listAgents(t, token) {
			if ag.Name == name && ag.Status == string(models.AgentStatusConnected) {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("agent %q did not report CONNECTED within %v", name, timeout)
		case <-time.After(250 * time.Millisecond):
		}
	}
}
