package tunnelmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/hoophq/hoop/common/grpc"

	"github.com/hoophq/hoop/tunnel/client"
	"github.com/hoophq/hoop/tunnel/daemonconfig"
)

// buildGatewayConfig converts a daemonconfig.Config into the gRPC
// client config the per-flow pipes use. Mirrors the equivalent helper
// that lived in cmd/hsh-tunneld/main.go before the lifecycle refactor.
//
// Auto-discovers the gRPC address from /api/serverinfo when not
// pinned via cfg.GrpcURL. TLSSkipVerify / TLSServerName come from
// the manager opts so the same envs the legacy code honoured
// continue to work.
func (m *Manager) buildGatewayConfig(ctx context.Context, cfg daemonconfig.Config, tokens TokenSource) (grpc.ClientConfig, string, error) {
	if cfg.APIURL == "" || cfg.Token == "" {
		return grpc.ClientConfig{}, "", errors.New("api_url and token are required")
	}
	apiBase := strings.TrimRight(cfg.APIURL, "/")

	grpcURL := cfg.GrpcURL
	if grpcURL == "" {
		token, epoch := tokens.Snapshot()
		si, err := client.FetchServerInfo(ctx, client.FetchServerInfoOptions{
			APIBaseURL: apiBase,
			Token:      token,
			OnNewToken: func(newToken string) { tokens.Rotate(newToken, epoch) },
		})
		if err != nil {
			return grpc.ClientConfig{}, "", fmt.Errorf("fetch serverinfo: %w", err)
		}
		grpcURL = si.GrpcURL
	}

	srvAddr, err := grpc.ParseServerAddress(grpcURL)
	if err != nil {
		return grpc.ClientConfig{}, "", fmt.Errorf("parse gRPC URL %q: %w", grpcURL, err)
	}
	currentToken, _ := tokens.Snapshot()
	return grpc.ClientConfig{
		ServerAddress: srvAddr,
		// Token here is the bring-up snapshot; per-flow dials overwrite
		// it with a fresh snapshot so rotations apply to new streams
		// (see makeTCPHandler).
		Token:    currentToken,
		Insecure: isInsecureScheme(grpcURL),
		// Honour the same env-var escape hatches the daemon honoured
		// pre-refactor. Wiring them through Options would be cleaner
		// but for dev/integration runs the env is what people set.
		TLSSkipVerify: m.opts.TLSSkipVerify || os.Getenv("HOOP_TLS_SKIP_VERIFY") == "true",
		TLSServerName: firstNonEmpty(m.opts.TLSServerName, os.Getenv("HOOP_TLSSERVERNAME")),
	}, apiBase, nil
}

// isInsecureScheme returns true for schemes that use plain-text gRPC:
// "http://" or "grpc://". Everything else (bare HOST:PORT, "https://",
// "grpcs://") implies TLS, matching the hoop CLI's hasInsecureScheme.
func isInsecureScheme(grpcURL string) bool {
	low := strings.ToLower(grpcURL)
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "grpc://")
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
