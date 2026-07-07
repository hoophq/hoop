package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hoophq/hoop/agent"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway"
	"github.com/hoophq/hoop/gateway/services"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/spf13/cobra"
)

const (
	standaloneDefaultAPIURL  = "http://127.0.0.1:8009"
	standaloneDefaultGrpcURL = "grpc://127.0.0.1:8010"
)

var startStandaloneCmd = &cobra.Command{
	Use:          "standalone",
	Short:        "Runs the gateway and a local agent in a single process",
	Long: `Runs the gateway and a local agent in a single process, suitable for
single-node deployments. When POSTGRES_DB_URI is not set, an embedded
PostgreSQL (pglite) is used with data stored under $HOME/.hoop/standalone.`,
	SilenceUsage: false,
	Run: func(cmd *cobra.Command, args []string) {
		if err := runStandalone(); err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	startCmd.AddCommand(startStandaloneCmd)
}

func runStandalone() error {
	if os.Getenv("ORG_MULTI_TENANT") == "true" {
		return fmt.Errorf("standalone mode is not supported with ORG_MULTI_TENANT=true")
	}
	if err := applyStandaloneDefaults(); err != nil {
		return err
	}

	// The gateway blocks forever serving the HTTP API; it fatals on its own
	// if the bootstrap fails, taking the whole process down (standalone has
	// no use for an agent without a gateway).
	go gateway.Run()

	apiURL := envOrDefault("API_URL", standaloneDefaultAPIURL)
	if err := waitGatewayHealthy(apiURL, 120*time.Second); err != nil {
		return err
	}
	log.Infof("gateway is up at %v", apiURL)

	// The provisioning logic lives in gateway/services so the integration
	// suite exercises the exact code path this command runs.
	grpcURL := envOrDefault("GRPC_URL", standaloneDefaultGrpcURL)
	dsn, err := services.StandaloneAgentDSN(grpcURL)
	if err != nil {
		return fmt.Errorf("failed provisioning the standalone agent credentials: %w", err)
	}

	// agent.Run loads its configuration from HOOP_KEY and blocks in the
	// connect/reconnect loop against the local gateway.
	os.Setenv("HOOP_KEY", dsn)
	log.Infof("starting standalone agent %q against the local gateway", services.StandaloneAgentName)
	agent.Run()
	return nil
}

// applyStandaloneDefaults points filesystem-dependent gateway settings at a
// per-user standalone home ($HOME/.hoop/standalone) when they are not
// explicitly configured:
//   - POSTGRES_DB_URI -> embedded pglite database under pgdata/
//   - session WAL (audit plugin) -> sessions/ instead of /opt/hoop/sessions
func applyStandaloneDefaults() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("unable to resolve the user home directory for the standalone data (set POSTGRES_DB_URI and PLUGIN_AUDIT_PATH to override): %v", err)
	}
	standaloneHome := filepath.Join(home, ".hoop", "standalone")

	if os.Getenv("POSTGRES_DB_URI") == "" {
		dataDir := filepath.Join(standaloneHome, "pgdata")
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return fmt.Errorf("failed creating the embedded database directory %v: %v", dataDir, err)
		}
		os.Setenv("POSTGRES_DB_URI", "pglite://"+dataDir)
		log.Infof("using embedded database, data-dir=%v", dataDir)
	}

	// PLUGIN_AUDIT_PATH is consumed at package init time, so the resolved
	// variable is adjusted directly when the env was not provided.
	if os.Getenv("PLUGIN_AUDIT_PATH") == "" {
		auditPath := filepath.Join(standaloneHome, "sessions")
		if err := os.MkdirAll(auditPath, 0o700); err != nil {
			return fmt.Errorf("failed creating the session storage directory %v: %v", auditPath, err)
		}
		plugintypes.AuditPath = auditPath
	}
	return nil
}

// waitGatewayHealthy polls the gateway liveness endpoint until it reports
// healthy. The endpoint also validates the gRPC listener, so a 200 means
// both servers are accepting connections.
func waitGatewayHealthy(apiURL string, timeout time.Duration) error {
	healthzURL := apiURL + "/api/healthz"
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		resp, err := client.Get(healthzURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gateway did not become healthy within %v polling %v", timeout, healthzURL)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
