// serverinfo.go fetches gateway metadata from the /api/serverinfo endpoint.
// The tunnel uses this to discover the gRPC URL so callers only need to
// supply HOOP_APIURL and HOOP_TOKEN — the same minimal set the hoop CLI
// requires after a successful login.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ServerInfo holds the fields from GET /api/serverinfo that the tunnel cares about.
type ServerInfo struct {
	// GrpcURL is the gRPC endpoint the client should dial, e.g.
	// "use.hoop.dev:8443" or "grpcs://use.hoop.dev:8443".
	GrpcURL string `json:"grpc_url"`

	// FeatureFlags is a map of feature flag name → enabled. The tunnel
	// uses this to check experimental.hoop_tunnel before proceeding.
	FeatureFlags map[string]bool `json:"feature_flags,omitempty"`
}

// FetchServerInfoOptions parametrizes the API call.
type FetchServerInfoOptions struct {
	// APIBaseURL is the gateway's HTTP API base, e.g. "https://hoop.dev"
	// or "http://127.0.0.1:8009". Must NOT include /api.
	APIBaseURL string

	// Token is the user's bearer token, same one used for gRPC.
	Token string

	// HTTPClient lets callers inject a client with custom TLS / proxies.
	// Defaults to a 15-second-timeout client.
	HTTPClient *http.Client
}

// FetchServerInfo calls GET /api/serverinfo and returns the gateway
// metadata. It fails fast if the response cannot be decoded or if
// grpc_url is absent, because the tunnel cannot operate without it.
func FetchServerInfo(ctx context.Context, opts FetchServerInfoOptions) (*ServerInfo, error) {
	if opts.APIBaseURL == "" {
		return nil, errors.New("client.FetchServerInfo: APIBaseURL is required")
	}
	if opts.Token == "" {
		return nil, errors.New("client.FetchServerInfo: Token is required")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	base, err := url.Parse(opts.APIBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse APIBaseURL: %w", err)
	}
	base.Path = strings.TrimSuffix(strings.TrimRight(base.Path, "/"), "/api") + "/api/serverinfo"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+opts.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", base.String(), err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// ok, fall through
	case http.StatusNotFound:
		return nil, fmt.Errorf("GET %s: gateway does not expose the serverinfo route", base.String())
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GET %s returned %s: %s", base.String(), resp.Status, strings.TrimSpace(string(body)))
	}

	var si ServerInfo
	if err := json.NewDecoder(resp.Body).Decode(&si); err != nil {
		return nil, fmt.Errorf("decode serverinfo response: %w", err)
	}
	if si.GrpcURL == "" {
		return nil, fmt.Errorf("serverinfo response is missing grpc_url")
	}
	return &si, nil
}
