package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a Go consumer for the hsh-tunneld control plane. It is a
// thin wrapper around net/http: tests, the daemon's own integration
// suite, and any future Go-side tooling (e.g. an admin CLI helper) use
// this rather than reimplementing the JSON marshalling.
//
// The hsh CLI (Bun/TypeScript) reimplements the same contract in TS.
// We do not generate code from this file; the OpenAPI document
// (openapi.yaml) is the cross-language source of truth.
type Client struct {
	httpClient *http.Client
	token      string
	// baseURL is always "http://hsh-tunneld" — the host part is
	// ignored by the unix-socket dialer but net/http requires a valid
	// URL. We keep it constant so callers don't accidentally point the
	// client at a real HTTP endpoint.
	baseURL string
}

// ClientOptions configures NewClient.
type ClientOptions struct {
	// SocketPath is the path of the local control-plane socket. On
	// Unix this is a filesystem path; on Windows it is a named-pipe
	// path of the form `\\.\pipe\<name>`. If empty, the platform
	// default is used (DefaultSocketPathUnix or DefaultSocketPathWindows).
	SocketPath string

	// Token is the control token (read from /var/run/hsh/control-token
	// or its Windows equivalent). Required. The client does not read
	// the token file itself — callers do that so they can also detect
	// "token missing → daemon not running" and surface the right UX.
	Token string

	// Timeout caps any single round-trip. Defaults to 15 seconds, which
	// is generous for a local socket and short enough to surface a hung
	// daemon to the UI without waiting forever.
	Timeout time.Duration
}

// NewClient builds a Client that talks to the daemon over a Unix
// socket / named pipe.
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.Token == "" {
		return nil, errors.New("ipc: NewClient: Token is required")
	}
	path := opts.SocketPath
	if path == "" {
		path = defaultClientSocketPath()
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	transport := &http.Transport{
		// DialContext ignores the network/addr arguments and connects
		// directly to the configured socket. The host part of the URL
		// (see baseURL below) is therefore irrelevant.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, socketNetwork(path), path)
		},
		// Disable connection reuse considerations that don't apply to
		// a local socket: no keep-alive idle limits, no TLS, no proxy.
		DisableCompression: true,
	}

	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		token:   opts.Token,
		baseURL: "http://hsh-tunneld",
	}, nil
}

// ----------------------------------------------------------------------
// public API — one method per spec endpoint
// ----------------------------------------------------------------------

// Status corresponds to GET /v1/status.
func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	var out StatusResponse
	err := c.do(ctx, http.MethodGet, "/v1/status", nil, &out)
	return out, err
}

// Connections corresponds to GET /v1/connections.
func (c *Client) Connections(ctx context.Context) ([]Connection, error) {
	var out ConnectionsResponse
	if err := c.do(ctx, http.MethodGet, "/v1/connections", nil, &out); err != nil {
		return nil, err
	}
	return out.Connections, nil
}

// LoginStart corresponds to POST /v1/login/start.
func (c *Client) LoginStart(ctx context.Context) (LoginStartResponse, error) {
	var out LoginStartResponse
	err := c.do(ctx, http.MethodPost, "/v1/login/start", nil, &out)
	return out, err
}

// LoginPoll corresponds to GET /v1/login/poll?state=<state>.
func (c *Client) LoginPoll(ctx context.Context, state string) (LoginPollResponse, error) {
	if state == "" {
		return LoginPollResponse{}, errors.New("ipc: LoginPoll: state is required")
	}
	var out LoginPollResponse
	path := "/v1/login/poll?state=" + url.QueryEscape(state)
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// LoginLocal corresponds to POST /v1/login/local. Returns nil on
// success (204 No Content); errors are typed APIError as usual.
func (c *Client) LoginLocal(ctx context.Context, req LoginLocalRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/login/local", req, nil)
}

// Logout corresponds to POST /v1/logout.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/logout", nil, nil)
}

// Config corresponds to GET /v1/config.
func (c *Client) Config(ctx context.Context) (ConfigResponse, error) {
	var out ConfigResponse
	err := c.do(ctx, http.MethodGet, "/v1/config", nil, &out)
	return out, err
}

// UpdateConfig corresponds to PUT /v1/config.
func (c *Client) UpdateConfig(ctx context.Context, req ConfigUpdateRequest) (ConfigResponse, error) {
	var out ConfigResponse
	err := c.do(ctx, http.MethodPut, "/v1/config", req, &out)
	return out, err
}

// Reconnect corresponds to POST /v1/reconnect.
func (c *Client) Reconnect(ctx context.Context) (ReconnectResponse, error) {
	var out ReconnectResponse
	err := c.do(ctx, http.MethodPost, "/v1/reconnect", nil, &out)
	return out, err
}

// Up corresponds to POST /v1/tunnel/up.
func (c *Client) Up(ctx context.Context) (TunnelUpResponse, error) {
	var out TunnelUpResponse
	err := c.do(ctx, http.MethodPost, "/v1/tunnel/up", nil, &out)
	return out, err
}

// Down corresponds to POST /v1/tunnel/down.
func (c *Client) Down(ctx context.Context) (TunnelDownResponse, error) {
	var out TunnelDownResponse
	err := c.do(ctx, http.MethodPost, "/v1/tunnel/down", nil, &out)
	return out, err
}

// ----------------------------------------------------------------------
// internals
// ----------------------------------------------------------------------

// do performs a single round-trip. body, if non-nil, is JSON-encoded
// and sent as the request body. out, if non-nil, receives the
// JSON-decoded response. Non-2xx responses are translated to a
// typed APIError so callers can distinguish 401 from 500 cleanly.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("ipc: marshal %s %s body: %w", method, path, err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("ipc: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ipc: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Read at most 64 KiB of the error body — enough for diagnostic
		// messages, capped so a misbehaving daemon can't OOM the client.
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		apiErr := &APIError{StatusCode: resp.StatusCode}
		if len(raw) > 0 {
			// Try to decode the structured error body; fall through on
			// failure so the raw payload is still surfaced.
			var e ErrorResponse
			if jsonErr := json.Unmarshal(raw, &e); jsonErr == nil && e.Error != "" {
				apiErr.Message = e.Error
				apiErr.Code = e.Code
			} else {
				apiErr.Message = strings.TrimSpace(string(raw))
			}
		}
		if apiErr.Message == "" {
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
		return apiErr
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		// Drain whatever the server sent so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("ipc: decode %s %s response: %w", method, path, err)
	}
	return nil
}

// APIError is returned by Client methods when the daemon responded with
// a non-2xx status. Consumers can use errors.As to inspect StatusCode
// and Code without string-matching.
type APIError struct {
	// StatusCode is the HTTP status code.
	StatusCode int

	// Code is the machine-readable identifier from ErrorResponse.Code
	// (e.g. "unauthorized", "not_implemented"). May be empty for older
	// daemons that don't set it.
	Code string

	// Message is the human-readable error from ErrorResponse.Error, or
	// the raw response body if it wasn't valid JSON.
	Message string
}

// Error implements the standard error interface.
func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("ipc: HTTP %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("ipc: HTTP %d: %s", e.StatusCode, e.Message)
}

// IsNotImplemented reports whether err is an APIError with status 501
// (i.e. the daemon advertises the endpoint via the spec but has not
// yet implemented it). The UI uses this to feature-flag UI affordances.
func IsNotImplemented(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotImplemented
	}
	return false
}

// IsUnauthorized reports whether err is an APIError with status 401.
// The UI uses this to drive token re-read from the token file (token
// likely rotated because the daemon restarted).
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusUnauthorized
	}
	return false
}
