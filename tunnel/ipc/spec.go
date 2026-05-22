// Package ipc defines the local HTTP/JSON control plane spoken by the
// hsh-tunneld daemon and consumed by the unprivileged `hsh` CLI (which
// lives in the hoophq/hsh repository).
//
// The contract is intentionally small and language-agnostic so it can be
// reimplemented in TypeScript without code generation:
//
//   - HTTP/1.1 over a local-only transport (Unix domain socket on
//     Linux/macOS, named pipe on Windows). Never reachable from the
//     network — the listener never binds to a TCP port.
//   - JSON request and response bodies for every endpoint.
//   - Bearer-token authentication via the standard `Authorization`
//     header. The token is rotated on every daemon restart and dropped
//     to a file the local user can read (see auth.go).
//   - All endpoints are versioned with a `/v1/` prefix so we can land
//     breaking changes additively without burning the existing UIs.
//
// The full spec is mirrored in openapi.yaml inside this package; both
// files must stay in lockstep.
package ipc

import "time"

// ----------------------------------------------------------------------
// /v1/status
// ----------------------------------------------------------------------

// StatusResponse is what GET /v1/status returns. It is the primary
// indicator the tray and dashboard poll to colour their status pill.
//
// Running == true means the netstack and gRPC pipes are accepting flows.
// LoggedIn == true means the daemon has a valid bearer token persisted
// for the gateway; both flags are independent (the daemon may be up but
// the user signed out).
type StatusResponse struct {
	// Running reports whether the daemon's tunnel loop is currently
	// active. False while the daemon is initialising or shutting down.
	Running bool `json:"running"`

	// LoggedIn reports whether a usable hoop access token is present in
	// the daemon-managed config file. It says nothing about whether the
	// gateway has *accepted* that token on the most recent connect.
	LoggedIn bool `json:"logged_in"`

	// Since is the time at which the daemon entered its current Running
	// state (or zero if never). Used by the UI to show "connected for
	// 3m". Encoded as RFC 3339.
	Since time.Time `json:"since,omitempty"`

	// LastError, if non-empty, is the most recent non-fatal error the
	// daemon hit (e.g. transient gRPC dial failure). Cleared on the next
	// successful operation. The UI surfaces this in the "Last error"
	// strip.
	LastError string `json:"last_error,omitempty"`

	// DaemonVersion is the build version of hsh-tunneld, matching
	// common/version.Get().Version. Lets the UI warn about
	// daemon/client skew.
	DaemonVersion string `json:"daemon_version"`
}

// ----------------------------------------------------------------------
// /v1/connections
// ----------------------------------------------------------------------

// Connection is one row in the connection list the UI renders. The set
// is exactly the connections the daemon would expose under `*.hoop` for
// the currently logged-in user — i.e. the result of filtering the
// gateway's /api/connections by tunnelable subtypes and allocating a
// virtual IP for each.
type Connection struct {
	// Name as it appears in the hoop gateway (no domain suffix).
	Name string `json:"name"`

	// SubType is the connection's hoop subtype: "postgres", "mysql",
	// "mssql", "mongodb", "oracledb", or "tcp". Used by the UI to render
	// a protocol badge and pick the right "Copy command" template.
	SubType string `json:"subtype"`

	// VirtualIP is the ULA IPv6 address inside the tunnel's /48 that
	// resolves to this connection. Stable for the daemon's current
	// session.
	VirtualIP string `json:"virtual_ip"`

	// ExpectedPort is the canonical TCP port the client is expected to
	// connect to (5432 for postgres, etc.). Zero for `tcp` subtype,
	// which accepts any user-defined upstream port.
	ExpectedPort uint16 `json:"expected_port"`
}

// ConnectionsResponse wraps the connection list in an object so we can
// add side-channel metadata later (e.g. pagination, refresh timestamp)
// without breaking consumers.
type ConnectionsResponse struct {
	Connections []Connection `json:"connections"`
}

// ----------------------------------------------------------------------
// /v1/login/{start,poll}
// ----------------------------------------------------------------------

// LoginStartResponse is returned by POST /v1/login/start. The UI must:
//  1. Render BrowserURL to the user (open it in their default browser).
//  2. Poll GET /v1/login/poll?state=<State> until the LoginPollResponse
//     reports Status == "done" or "error".
//
// The daemon binds its own loopback callback listener internally, so
// the UI never sees the redirect URI directly.
type LoginStartResponse struct {
	// BrowserURL is the gateway OIDC URL the user must open in a
	// browser to authenticate. Includes the daemon's loopback callback
	// in its `redirect` query parameter.
	BrowserURL string `json:"browser_url"`

	// State is the opaque token the UI must pass back to /v1/login/poll
	// to track this login attempt. Bound to the same redirect URI so a
	// stale poll cannot consume a fresh callback.
	State string `json:"state"`
}

// LoginPollStatus is the lifecycle of a login attempt.
type LoginPollStatus string

const (
	// LoginStatusPending is returned while the daemon is still waiting
	// for the browser callback. The UI should keep polling.
	LoginStatusPending LoginPollStatus = "pending"

	// LoginStatusDone is returned after the callback fired and the
	// daemon successfully persisted the token. The UI should refresh
	// /v1/status to show the new logged-in state.
	LoginStatusDone LoginPollStatus = "done"

	// LoginStatusError is returned if the callback delivered an error,
	// the state expired, or the daemon failed to persist the token.
	// Error carries a human-readable explanation; the UI should stop
	// polling and surface the message.
	LoginStatusError LoginPollStatus = "error"
)

// LoginPollResponse is returned by GET /v1/login/poll?state=<state>.
type LoginPollResponse struct {
	// Status reports where the login attempt is in its lifecycle. See
	// LoginPollStatus for the possible values.
	Status LoginPollStatus `json:"status"`

	// Error is set only when Status == "error". Empty otherwise.
	Error string `json:"error,omitempty"`
}

// ----------------------------------------------------------------------
// /v1/config
// ----------------------------------------------------------------------

// ConfigResponse is the daemon-managed configuration as seen by the UI.
// Excludes the access token: tokens are write-only via the login flow.
type ConfigResponse struct {
	// APIURL is the hoop gateway's HTTPS API base, e.g.
	// "https://hoop.example.com". Empty if the daemon has not been
	// configured yet.
	APIURL string `json:"api_url"`

	// GrpcURL is an optional override for the gRPC address (otherwise
	// discovered from /api/serverinfo). Empty unless the user pinned it.
	GrpcURL string `json:"grpc_url,omitempty"`

	// LogLevel is one of "debug", "info", "warn", "error". The daemon
	// honours this on the next reconnect.
	LogLevel string `json:"log_level"`
}

// ConfigUpdateRequest is what PUT /v1/config accepts. Each field is
// optional; omitted fields are left untouched on the daemon side. The
// access token is never settable this way — use the login flow.
type ConfigUpdateRequest struct {
	APIURL   *string `json:"api_url,omitempty"`
	GrpcURL  *string `json:"grpc_url,omitempty"`
	LogLevel *string `json:"log_level,omitempty"`
}

// ----------------------------------------------------------------------
// /v1/reconnect
// ----------------------------------------------------------------------

// ReconnectResponse is returned by POST /v1/reconnect. The daemon
// acknowledges the request synchronously (HTTP 202) and tears down +
// reopens the tunnel asynchronously. The UI watches /v1/status for the
// state transition.
type ReconnectResponse struct {
	// Accepted is always true on a 202 response. Present for parity
	// with other endpoints that need a JSON body and for forward-compat
	// (we may carry a request-id later).
	Accepted bool `json:"accepted"`
}

// ----------------------------------------------------------------------
// Errors
// ----------------------------------------------------------------------

// ErrorResponse is the body for any non-2xx response from the control
// plane. Status codes follow standard HTTP semantics:
//
//   - 400: malformed request (bad JSON, missing required field).
//   - 401: missing or invalid bearer token.
//   - 404: unknown route or referenced object (e.g. unknown state in
//     /v1/login/poll).
//   - 409: state conflict (e.g. starting a login while one is pending).
//   - 500: daemon-side bug; details in Error.
//   - 501: endpoint defined in the spec but not yet implemented in this
//     daemon build.
//   - 503: daemon is shutting down.
type ErrorResponse struct {
	// Error is a short human-readable description. Stable across
	// versions for known failure modes so the UI can match on it.
	Error string `json:"error"`

	// Code is a stable machine-readable identifier ("unauthorized",
	// "not_implemented", etc.). Optional; HTTP status alone is enough
	// for most clients.
	Code string `json:"code,omitempty"`
}
