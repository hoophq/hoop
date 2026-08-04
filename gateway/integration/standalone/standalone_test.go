//go:build integration

package standalone

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/agent/controller"
	"github.com/hoophq/hoop/common/clientconfig"
	commongrpc "github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
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

// TestServerLogsAgentShipping proves the agent→gateway server-logs path end
// to end with a live transport: the production agent controller connects
// over gRPC, its log shipper picks up new local log entries and ships them
// as ClientAgentLogs packets, the transport intercept stores them, and both
// the /server-logs snapshot and the /server-logs/stream SSE endpoint expose
// them. Runs after TestStandaloneLifecycle in this file by design (shares
// the registered first user).
func TestServerLogsAgentShipping(t *testing.T) {
	token := adminToken(t)

	dsn, err := services.StandaloneAgentDSN("grpc://" + gw.GRPCAddr)
	if err != nil {
		t.Fatalf("provisioning standalone agent: %v", err)
	}
	client, err := commongrpc.Connect(commongrpc.ClientConfig{
		ServerAddress: gw.GRPCAddr,
		Token:         dsn,
		UserAgent:     "hoop-serverlogs-itest",
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
	waitAgentConnected(t, token, services.StandaloneAgentName, 30*time.Second)

	// Open the SSE stream BEFORE emitting the marker with backlog disabled:
	// any marker frame received later is necessarily live-follow, not replay.
	stream := gw.HTTP.Get(t, "/server-logs/stream?backlog=0", token)
	t.Cleanup(func() { stream.Body.Close() })
	testutil.RequireStatus(t, stream, http.StatusOK)
	if ct := stream.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("stream: expected text/event-stream content type, got %q", ct)
	}

	// The marker is logged after the shipper's start cursor, so the next 2s
	// tick ships it over the live gRPC stream.
	marker := fmt.Sprintf("serverlogs-e2e-marker-%d", time.Now().UnixNano())
	log.Infof("standalone server-logs shipping check %s", marker)

	// Snapshot endpoint: the marker must surface with source=agent and the
	// authenticated agent identity attached by the transport intercept.
	deadline := time.Now().Add(15 * time.Second)
	for {
		if entry := findAgentLogEntry(t, token, marker); entry != nil {
			if name, _ := (*entry)["agent_name"].(string); name != services.StandaloneAgentName {
				t.Fatalf("agent log entry: expected agent_name=%q, got %v", services.StandaloneAgentName, (*entry)["agent_name"])
			}
			if id, _ := (*entry)["agent_id"].(string); id == "" {
				t.Fatalf("agent log entry: missing agent_id: %v", *entry)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("marker log entry with source=agent did not reach /server-logs within 15s")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Stream endpoint: the marker must also arrive as a live SSE frame.
	frames := make(chan map[string]any, 16)
	go func() {
		defer close(frames)
		scanner := bufio.NewScanner(stream.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var entry map[string]any
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &entry) == nil {
				frames <- entry
			}
		}
	}()
	streamDeadline := time.After(15 * time.Second)
	for {
		select {
		case entry, ok := <-frames:
			if !ok {
				t.Fatal("SSE stream closed before the marker frame arrived")
			}
			if msg, _ := entry["message"].(string); strings.Contains(msg, marker) {
				return
			}
		case <-streamDeadline:
			t.Fatal("marker frame did not arrive on /server-logs/stream within 15s")
		}
	}
}

// findAgentLogEntry fetches the server-logs snapshot and returns the first
// source=agent entry whose message contains marker, or nil.
func findAgentLogEntry(t *testing.T, token, marker string) *map[string]any {
	t.Helper()
	resp := gw.HTTP.Get(t, "/server-logs?limit=5000", token)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusOK)
	var entries []map[string]any
	testutil.DecodeJSON(t, resp, &entries)
	for _, entry := range entries {
		msg, _ := entry["message"].(string)
		if entry["source"] == "agent" && strings.Contains(msg, marker) {
			return &entry
		}
	}
	return nil
}

// adminToken returns a JWT for the default admin user whether or not a prior
// test in this package already registered it.
func adminToken(t *testing.T) string {
	t.Helper()
	resp := gw.HTTP.Post(t, "/localauth/register", "", openapi.LocalUserRequest{
		Email:    testutil.FirstUserEmail,
		Password: testutil.FirstUserPassword,
		Name:     testutil.FirstUserName,
	})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		if token := resp.Header.Get("Token"); token != "" {
			return token
		}
	}
	return testutil.Login(t, gw.HTTP, testutil.FirstUserEmail, testutil.FirstUserPassword)
}
