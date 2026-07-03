// connections.go fetches the list of hoop connections from the gateway
// over its existing REST API. The tunnel does not introduce any new
// gateway endpoints: it asks the same `GET /api/connections` the webapp
// uses, then filters for connection types it can actually carry.
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

	pb "github.com/hoophq/hoop/common/proto"
)

// Connection is the minimal projection of the gateway's openapi.Connection
// struct that the tunnel cares about. We never deserialize secrets,
// schedules, reviews, etc.
type Connection struct {
	Name    string // the lookup key for `hoop connect`
	SubType string // postgres, mysql, mssql, mongodb, tcp, ...
}

// FetchConnectionsOptions parametrizes the API call.
type FetchConnectionsOptions struct {
	// APIBaseURL is the gateway's HTTP API base, e.g. "https://hoop.dev"
	// or "http://127.0.0.1:8009". Must NOT include /api.
	APIBaseURL string

	// Token is the user's bearer token, same one used for gRPC.
	Token string

	// HTTPClient lets callers inject a client with custom TLS / proxies.
	// Defaults to a 15-second-timeout client.
	HTTPClient *http.Client
}

// FetchConnections returns the list of connections available to the
// current user that are tunnelable (TCP-style protocols). Connections
// that are not tunnelable (SSH, command-line, http-proxy, kubernetes,
// RDP, SSM, etc.) are silently filtered out — they need a protocol-
// specific client, not a transparent IP tunnel.
func FetchConnections(ctx context.Context, opts FetchConnectionsOptions) ([]Connection, error) {
	if opts.APIBaseURL == "" {
		return nil, errors.New("client.FetchConnections: APIBaseURL is required")
	}
	if opts.Token == "" {
		return nil, errors.New("client.FetchConnections: Token is required")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	base, err := url.Parse(opts.APIBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse APIBaseURL: %w", err)
	}
	// Strip a trailing /api if the caller accidentally included it, then
	// build the canonical path. This is friendlier than failing on a
	// trivial config mistake.
	base.Path = strings.TrimSuffix(strings.TrimRight(base.Path, "/"), "/api") + "/api/connections"

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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GET %s returned %s: %s", base.String(), resp.Status, strings.TrimSpace(string(body)))
	}

	// The non-paginated endpoint returns a flat array of openapi.Connection.
	// We decode only the two fields we need so this code is robust to
	// upstream schema additions.
	var raw []struct {
		Name    string `json:"name"`
		SubType string `json:"subtype"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	out := make([]Connection, 0, len(raw))
	for _, r := range raw {
		if r.Name == "" {
			continue
		}
		if !isTunnelableSubType(r.SubType) {
			continue
		}
		out = append(out, Connection{Name: r.Name, SubType: r.SubType})
	}
	return out, nil
}

// isTunnelableSubType mirrors pipe.go's isTunnelableType but works on
// the raw openapi subtype field.
func isTunnelableSubType(subtype string) bool {
	switch pb.ConnectionType(subtype) {
	case pb.ConnectionTypePostgres,
		pb.ConnectionTypeMySQL,
		pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMongoDB,
		pb.ConnectionTypeOracleDB,
		pb.ConnectionTypeTCP:
		return true
	}
	return false
}
