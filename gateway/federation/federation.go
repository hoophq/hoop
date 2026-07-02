// Package federation resolves per-session cloud credentials by impersonating
// the calling user's IAM principal. The package is invoked at SessionOpen time
// (see gateway/transport/client.go) so that the env vars returned by a
// Resolver are merged into AgentConnectionParams.EnvVars before the gob-encode
// step. The agent sees only the resulting short-lived credentials; the admin
// service-account credentials configured by the customer never leave the
// gateway process for built-in providers.
//
// Adding a new provider:
//
//  1. Implement Resolver in its own subpackage (e.g. gateway/federation/awssts).
//  2. Wire it into the registry in NewResolver.
//  3. Add the provider constant to models/connection_federation.go and update
//     the migration's CHECK constraint if it gates provider names.
package federation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/gateway/models"
)

// ErrUserNotConnected signals that a per-user federation provider has no stored
// credential for the calling user — i.e. the user has not completed the
// provider's consent flow (e.g. gcp_oauth's Google authorization). The
// session-open path detects this via errors.Is and tags the precondition error
// with a stable, machine-readable code so clients can render a "connect your
// account" action instead of surfacing a raw error string. Resolvers should
// wrap it (fmt.Errorf("...: %w", federation.ErrUserNotConnected)) so the
// human-readable context is preserved alongside the sentinel.
var ErrUserNotConnected = errors.New("user has not connected an account for this connection")

// OAuthNotConnectedCode is the stable, machine-readable marker the session-open
// path embeds in the precondition error when a per-user federation provider
// (gcp_oauth) has no stored credential for the calling user. Clients match on
// this code — not the human-readable text — so the detection survives wording
// changes. The full tag is rendered as
// "[code=oauth_not_connected connection=<name>]".
//
// This constant is the single source of truth for the contract shared by the
// producer (gateway/transport/client.go), the CLI (client/cmd), and the MCP
// server (gateway/api/mcpserver). Do not duplicate it.
const OAuthNotConnectedCode = "code=oauth_not_connected"

// FormatOAuthNotConnected renders the stable, machine-readable tag the
// session-open path appends to the precondition error when ErrUserNotConnected
// is hit, embedding the connection name so clients can name the resource and
// fetch its consent URL.
func FormatOAuthNotConnected(connectionName string) string {
	return fmt.Sprintf("[%s connection=%s]", OAuthNotConnectedCode, connectionName)
}

// ParseOAuthNotConnected reports whether errMsg carries the
// OAuthNotConnectedCode marker and, when present, extracts the connection name
// from the `connection=<name>` tag. It works whether errMsg is a raw rpc error
// string or an HTTP error body that embeds it. ok is true whenever the code is
// present, even if the connection name could not be recovered.
func ParseOAuthNotConnected(errMsg string) (connectionName string, ok bool) {
	if !strings.Contains(errMsg, OAuthNotConnectedCode) {
		return "", false
	}
	const marker = "connection="
	idx := strings.Index(errMsg, marker)
	if idx == -1 {
		return "", true
	}
	rest := errMsg[idx+len(marker):]
	// The tag is rendered as "[code=oauth_not_connected connection=<name>]";
	// the name ends at the first space or closing bracket.
	if end := strings.IndexAny(rest, " ]"); end != -1 {
		return strings.TrimSpace(rest[:end]), true
	}
	return strings.TrimSpace(rest), true
}

// Result is the resolved per-session output of a federation provider. Callers
// must merge EnvVars into the connection's secret map (base64-encoding values)
// before propagating them to the agent.
type Result struct {
	// EnvVars are the environment variables the agent should inject into the
	// session command. Values are plaintext; the SessionOpen path is
	// responsible for base64-encoding to match the wire contract enforced by
	// agent/secretsmanager.Decode.
	EnvVars map[string]string

	// SupersededEnvVars lists static connection env-var NAMES (without any
	// "envvar:"/"filesystem:" prefix) whose presence on the connection is
	// made redundant by EnvVars. The session-open path removes these from
	// the connection's secret map before propagating to the agent so
	// federated and legacy credentials cannot coexist at runtime.
	//
	// Example: the gcp_iam provider emits HOOP_GCP_ACCESS_TOKEN and lists
	// GOOGLE_APPLICATION_CREDENTIALS here, because the agent-side bq
	// wrapper prefers the federated token and the legacy key file becomes
	// dead weight (and a confusing co-existence warning) once federation
	// is in place.
	//
	// Provider authors must keep this list narrow: only the env vars the
	// provider's output truly supersedes belong here. Stripping unrelated
	// connection envs would silently break sessions for the customer.
	SupersededEnvVars []string

	// ResolvedPrincipal is the cloud-side identity the user was impersonated
	// as. Recorded in session metadata for audit (PRD v1.1 surfaces it).
	ResolvedPrincipal string

	// AdminPrincipal is the impersonator identity (e.g. the admin SA's
	// client_email). Recorded in session metadata so admins can correlate the
	// gateway-side SA with the GCP audit trail.
	AdminPrincipal string

	// TokenExpiresAt is the expiration of the short-lived credential the
	// session will use. The agent may run beyond this; the credential's own
	// expiry is the source of truth.
	TokenExpiresAt time.Time
}

// ResolveRequest is the input every Resolver receives. The federation service
// builds this from the user/session/connection context and the decrypted
// admin credentials. Resolvers must not retain references to
// AdminCredentialsPlain past Resolve returning.
type ResolveRequest struct {
	OrgID        string
	ConnectionID string

	// UserID and UserEmail come from storagev2.Context. For MCP traffic this
	// is the Entra-verified identity, for web traffic it is the
	// OIDC-resolved Hoop user.
	UserID    string
	UserEmail string

	// Config is the persisted federation row. Provider implementations read
	// IdentityTargetTemplate, TokenTTLSeconds, ExtraConfig (e.g. project_id)
	// from it.
	Config *models.ConnectionFederationConfig

	// AdminCredentialsPlain is the decrypted admin credential blob. Its shape
	// is provider-specific: the GCP service-account JSON for gcp_iam, or the
	// OAuth client config (client_id/client_secret JSON) for gcp_oauth. The
	// caller is responsible for zeroing this when no longer needed.
	AdminCredentialsPlain []byte

	// UserCredentialsPlain is the decrypted PER-USER credential blob, loaded by
	// the federation service from the per-user credential store keyed by
	// (connection, user). Providers that mint tokens from a user-supplied
	// credential read it here: gcp_oauth uses it for the Google refresh token.
	// Empty for providers that have no per-user credential (e.g. gcp_iam) or
	// when the user has not completed a required consent flow — resolvers must
	// treat empty as an actionable "not connected" condition. The caller is
	// responsible for zeroing this when no longer needed.
	UserCredentialsPlain []byte

	// ResolvedPrincipal is the target principal computed by the identity
	// mapping engine (e.g. "user@acme.com"). Passed explicitly rather than
	// recomputed inside the resolver so dry-run mode can override it.
	ResolvedPrincipal string
}

// Resolver is implemented by each cloud-specific federation provider. The
// contract is:
//
//   - Resolve is invoked synchronously on every SessionOpen for a federated
//     connection, so it must return quickly (seconds, not minutes).
//   - Errors propagate to the session-open path, which applies the configured
//     fallback policy.
//   - Resolvers must not log or persist the resolved credentials.
type Resolver interface {
	// Provider returns the stable identifier of this provider. Used to look
	// up the resolver from the registry; must match
	// ConnectionFederationConfig.BuiltinProvider on disk.
	Provider() string

	// Resolve produces the env vars and audit metadata for a single session.
	Resolve(ctx context.Context, req ResolveRequest) (*Result, error)
}

// registry holds the process-wide set of built-in resolvers. Registered at
// package init time by each provider subpackage via Register.
var registry = map[string]Resolver{}

// Register installs a resolver in the process-wide registry. Panics on
// duplicate registration so wiring mistakes fail loudly at startup.
func Register(r Resolver) {
	if r == nil {
		panic("federation: cannot register nil resolver")
	}
	name := r.Provider()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("federation: resolver %q already registered", name))
	}
	registry[name] = r
}

// LookupResolver returns the Resolver registered for the given provider name,
// or an error when no resolver is registered. The error is intentionally
// human-readable since it surfaces in API responses (PRD §5.3 wants verbatim
// failures).
func LookupResolver(name string) (Resolver, error) {
	if name == "" {
		return nil, fmt.Errorf("no provider configured")
	}
	r, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("federation provider %q is not supported", name)
	}
	return r, nil
}
