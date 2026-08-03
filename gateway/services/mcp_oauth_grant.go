// MCP Gateway OAuth grant lifecycle.
//
// An MCP Gateway (mcpproxy) connection authenticates to an OAuth-protected
// remote MCP server with a credential the gateway brokered on an admin's
// behalf. Historically that credential was frozen into the connection's
// HEADER_AUTHORIZATION env var at create time and never renewed: the refresh
// token was obtained, handed to the browser once, and dropped. The connection
// then broke silently the moment the provider's access-token TTL elapsed.
//
// This file closes that gap for the mcpproxy subtype:
//
//   - AdoptMCPOAuthGrant promotes a completed login flow into a durable grant
//     keyed by the connection it authorized (called when that connection is
//     saved, since no connection row exists while the admin is authorizing),
//     and refuses a flow that authorized a different MCP server than the one
//     that connection is configured to reach.
//   - ResolveMCPOAuthHeader renews the access token at session open and hands
//     back the Authorization header value for that session, dropping the grant
//     when the provider rejects the refresh token outright.
//
// The agent is deliberately untouched. It still receives an ordinary static
// HEADER_AUTHORIZATION and still refuses MCP_AUTH=oauth, because from its
// point of view nothing changed: the gateway resolves the credential before
// the session starts, exactly as it does for identity federation. Renewal is
// therefore per session open rather than mid-session — a session outliving the
// token still breaks at the TTL, but every new session gets a live credential
// instead of a permanently dead one.
package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/mcpproxy/auth/outbound"
	"github.com/hoophq/mcpproxy/auth/outbound/oauth"
	"gorm.io/gorm"
)

// MCPOAuthGrantSubType is the connection subtype this credential lifecycle
// applies to. The legacy "mcp" subtype keeps the frozen-header behavior: it
// runs through the byte-relay httpproxy path, which has no notion of a grant.
const MCPOAuthGrantSubType = "mcpproxy"

// mcpGrantRefreshTimeout bounds the token-endpoint call made while a grant row
// is locked. Tight on purpose: the lock serializes every replica opening a
// session on this connection, and a session that waits minutes on a stuck
// authorization server is worse than one that fails fast.
const mcpGrantRefreshTimeout = 15 * time.Second

// mcpGrantExpiryMargin renews a token that is about to expire rather than one
// that already has. A credential resolved at session open is used for the life
// of the session, so handing out a token with two seconds left guarantees the
// first tool call fails.
const mcpGrantExpiryMargin = 2 * time.Minute

// mcpProxyEndpointEnvKey is where an mcpproxy connection's remote MCP endpoint
// lives in the connection secret map. The agent reads the same key
// (agent/controller: REMOTE_URL) and the webapp writes it there, so this is
// the connection's endpoint of record.
const mcpProxyEndpointEnvKey = "envvar:REMOTE_URL"

// AdoptMCPOAuthGrant promotes a completed OAuth login flow into a durable
// grant for a connection.
//
// The login runs before the connection exists (the admin authorizes while
// filling the create form), so the flow row cannot be keyed by connection at
// the time it is written. The connection save is the first moment both halves
// are known, and this is where they are joined. The flow row is consumed:
// after this returns the grant owns the tokens.
//
// Re-authorizing an existing connection overwrites its grant, which is what an
// admin clicking "Re-authorize" means.
//
// connEnvs is the saved connection's secret map, and the flow is refused
// unless the server it authorized is the endpoint that connection points at.
// Nothing binds the two otherwise: an admin can authorize against server A,
// edit the URL field to server B, and save — the flow id in the payload still
// points at A. Adopting it would durably attach A's refresh token to a
// connection that talks to B and auto-renew it forever, which is credential
// exfiltration performed by editing a form field.
func AdoptMCPOAuthGrant(orgID, connectionID, flowID string, connEnvs map[string]string) error {
	flow, err := models.GetMCPOAuthFlow(models.DB, flowID)
	if err != nil {
		return fmt.Errorf("failed loading oauth flow: %w", err)
	}
	// A flow id is a random UUID, but scoping to the org keeps a leaked id
	// from attaching another org's credential to this connection.
	if flow.OrgID != orgID {
		return fmt.Errorf("oauth flow does not belong to this organization")
	}
	switch flow.Status {
	case models.MCPOAuthFlowStatusCompleted, models.MCPOAuthFlowStatusConsumed:
		// Completed means the browser has not redeemed the token yet;
		// consumed means it has and the access token was scrubbed. Both carry
		// the refresh token and the endpoint identity a grant needs, and the
		// access token is optional here: the first session refreshes it.
	default:
		return fmt.Errorf("oauth flow has not completed (status=%s)", flow.Status)
	}
	if len(flow.RefreshTokenEncrypted) == 0 && len(flow.AccessTokenEncrypted) == 0 {
		return fmt.Errorf("oauth flow carries no credential to adopt")
	}
	if err := checkGrantEndpointMatch(flow.ServerURL, connEnvs); err != nil {
		return err
	}

	grant := &models.MCPOAuthGrant{
		ID:           uuid.NewString(),
		OrgID:        orgID,
		ConnectionID: connectionID,
		// Empty: the current UI brokers one credential shared by every user of
		// the connection. Per-user grants add rows here, they do not change
		// this one.
		UserID:                "",
		ServerURL:             flow.ServerURL,
		Resource:              flow.Resource,
		Issuer:                flow.Issuer,
		TokenEndpoint:         flow.TokenEndpoint,
		ClientID:              flow.ClientID,
		ClientSecretEncrypted: flow.ClientSecretEncrypted,
		TokenAuthMethod:       flow.TokenAuthMethod,
		Scopes:                flow.Scopes,
		AccessTokenEncrypted:  flow.AccessTokenEncrypted,
		RefreshTokenEncrypted: flow.RefreshTokenEncrypted,
		TokenType:             flow.TokenType,
		TokenExpiresAt:        flow.TokenExpiresAt,
	}
	if err := models.UpsertMCPOAuthGrant(models.DB, grant); err != nil {
		return fmt.Errorf("failed persisting oauth grant: %w", err)
	}
	if err := models.DeleteMCPOAuthFlow(models.DB, flow.ID); err != nil {
		log.Warnf("mcp oauth: failed deleting adopted flow %s: %v", flow.ID, err)
	}
	return nil
}

// checkGrantEndpointMatch refuses a flow that authorized a different MCP
// server than the connection is configured to reach.
//
// The comparison is on normalized scheme + host + path, not raw strings: the
// admin types the URL into one form field and the OAuth request may carry it
// back with a trailing slash, a differently-cased host, or an explicit default
// port, and refusing those would break honest saves. Everything that could
// change which server is reached stays significant — a different host, port,
// scheme or path is a different endpoint, and query and fragment are dropped
// because neither routes the request.
//
// A connection with no endpoint configured is refused too. The mcpproxy agent
// requires REMOTE_URL for every HTTP transport, so its absence means either a
// stdio connection (which has no OAuth server to authorize against) or a
// half-written payload; either way there is nothing to validate the flow
// against, and adopting on an unverifiable match is the bug this prevents.
func checkGrantEndpointMatch(flowServerURL string, connEnvs map[string]string) error {
	encoded := connEnvs[mcpProxyEndpointEnvKey]
	if encoded == "" {
		return fmt.Errorf("the connection has no %s configured, so the oauth login cannot be matched to it",
			strings.TrimPrefix(mcpProxyEndpointEnvKey, "envvar:"))
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("the connection's MCP endpoint is not decodable, so the oauth login cannot be matched to it")
	}
	connEndpoint, err := normalizeMCPEndpoint(string(raw))
	if err != nil {
		return fmt.Errorf("the connection's MCP endpoint is not a usable URL: %w", err)
	}
	flowEndpoint, err := normalizeMCPEndpoint(flowServerURL)
	if err != nil {
		return fmt.Errorf("the oauth login recorded an unusable server URL: %w", err)
	}
	if connEndpoint != flowEndpoint {
		return fmt.Errorf("the oauth login authorized %s but the connection points at %s; "+
			"re-authorize against the connection's own endpoint", flowEndpoint, connEndpoint)
	}
	return nil
}

// normalizeMCPEndpoint reduces an MCP server URL to the identity that decides
// which server the bytes reach: lowercase scheme and host, default port
// elided, one trailing slash removed, query and fragment dropped.
func normalizeMCPEndpoint(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", fmt.Errorf("%q is not an absolute URL", rawURL)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" && !isDefaultPort(scheme, port) {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host + strings.TrimSuffix(u.EscapedPath(), "/"), nil
}

func isDefaultPort(scheme, port string) bool {
	return (scheme == "https" && port == "443") || (scheme == "http" && port == "80")
}

// errMCPGrantRejected signals out of the resolve transaction that the
// authorization server rejected the stored refresh token, so the grant row is
// dead and must be deleted.
//
// It exists because the deletion cannot happen where the rejection is
// detected: resolveGrantHeader runs inside the locked transaction and returns
// an error, which rolls back everything that transaction did — a DELETE
// included. Carrying the fact out as a sentinel lets the rollback happen
// cleanly and the delete run after it, on its own connection.
var errMCPGrantRejected = errors.New("mcp oauth: authorization server rejected the stored credential")

// ResolveMCPOAuthHeader returns the Authorization header value for an
// mcpproxy connection's stored grant, renewing the access token when it is
// expired or about to be.
//
// The empty string with a nil error means "this connection has no grant":
// every MCP connection that authenticates with a pasted token or nothing at
// all takes that path, and it must not disturb the session.
//
// The read-refresh-write cycle runs inside one transaction holding a row lock,
// so concurrent session opens across gateway replicas serialize. Without it,
// an authorization server that rotates refresh tokens would invalidate the
// loser's copy and break the grant permanently.
//
// A refresh the provider answers with invalid_grant is the one outcome that
// survives the rollback: the credential is dead, so the row is deleted here,
// after the transaction has unwound. Deleting inside it would be undone by
// the very error that reports the rejection, leaving the gateway to replay a
// refresh token the provider already refused on every subsequent session open
// — forever, and silently.
func ResolveMCPOAuthHeader(ctx context.Context, orgID, connectionID string) (string, error) {
	var header string
	var rejectedGrantID string
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		grant, err := models.LockMCPOAuthGrant(tx, orgID, connectionID, "")
		if err != nil {
			return err
		}
		header, err = resolveGrantHeader(ctx, tx, grant)
		if errors.Is(err, errMCPGrantRejected) {
			rejectedGrantID = grant.ID
		}
		return err
	})
	if rejectedGrantID != "" {
		if derr := models.DeleteMCPOAuthGrant(models.DB, rejectedGrantID); derr != nil {
			log.Warnf("mcp oauth: failed deleting rejected grant %s: %v", rejectedGrantID, derr)
		}
	}
	if errors.Is(err, models.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return header, nil
}

// resolveGrantHeader serves the stored access token while it is still good and
// runs the refresh-token grant otherwise. Runs with the grant row locked.
//
// A provider answering invalid_grant means the stored refresh token is dead,
// which the caller must act on by deleting the grant. That deletion cannot
// happen here: this function's error rolls the enclosing transaction back and
// would take the DELETE with it. The rejection is reported as
// errMCPGrantRejected instead, and ResolveMCPOAuthHeader deletes the row once
// the rollback is done.
func resolveGrantHeader(ctx context.Context, tx *gorm.DB, grant *models.MCPOAuthGrant) (string, error) {
	accessToken, err := decryptOptional(grant.AccessTokenEncrypted)
	if err != nil {
		return "", fmt.Errorf("failed decrypting access token: %w", err)
	}
	if accessToken != "" && !mcpGrantNeedsRefresh(grant, time.Now()) {
		return authorizationHeader(grant.TokenType, accessToken), nil
	}

	refreshToken, err := decryptOptional(grant.RefreshTokenEncrypted)
	if err != nil {
		return "", fmt.Errorf("failed decrypting refresh token: %w", err)
	}
	if refreshToken == "" {
		// Nothing to renew with. Serve what we have and let the upstream
		// reject it: an expired token produces a 401 the admin can act on,
		// while failing the session here would break a connection whose
		// provider issues long-lived tokens with an unreliable expires_in.
		if accessToken != "" {
			return authorizationHeader(grant.TokenType, accessToken), nil
		}
		return "", fmt.Errorf("grant has neither a usable access token nor a refresh token; re-authorize the connection")
	}

	clientSecret, err := decryptOptional(grant.ClientSecretEncrypted)
	if err != nil {
		return "", fmt.Errorf("failed decrypting client secret: %w", err)
	}

	refreshCtx, cancel := context.WithTimeout(ctx, mcpGrantRefreshTimeout)
	defer cancel()

	fresh, err := refreshMCPToken(refreshCtx, mcpTokenClient(grant, clientSecret), grant.TokenAuthMethod, refreshToken)
	if err != nil {
		var tokenErr *oauth.TokenError
		if errors.As(err, &tokenErr) && tokenErr.InvalidGrant() {
			return "", fmt.Errorf("%w; re-authorize the connection: %w", errMCPGrantRejected, err)
		}
		return "", fmt.Errorf("failed refreshing oauth token: %w", err)
	}

	if err := persistRefreshedGrant(tx, grant, fresh); err != nil {
		return "", err
	}
	return authorizationHeader(fresh.TokenType, fresh.AccessToken), nil
}

// persistRefreshedGrant writes a renewed token back onto the locked grant row.
//
// A refresh response that omits refresh_token means "keep using the one you
// have" (RFC 6749 §6 makes the field OPTIONAL), so an empty fresh.RefreshToken
// leaves the stored ciphertext alone. Overwriting it with nothing would erase
// the only credential that can renew this grant: the next session would find
// no refresh token, serve the stale access token until it expires, and then
// fail with "re-authorize the connection" — for a grant the provider never
// revoked.
func persistRefreshedGrant(tx *gorm.DB, grant *models.MCPOAuthGrant, fresh *outbound.Token) error {
	accessCipher, err := models.EncryptCredentialSecretKey(fresh.AccessToken)
	if err != nil {
		return fmt.Errorf("failed encrypting refreshed access token: %w", err)
	}
	grant.AccessTokenEncrypted = accessCipher
	if fresh.RefreshToken != "" {
		refreshCipher, err := models.EncryptCredentialSecretKey(fresh.RefreshToken)
		if err != nil {
			return fmt.Errorf("failed encrypting refreshed refresh token: %w", err)
		}
		grant.RefreshTokenEncrypted = refreshCipher
	}
	if fresh.TokenType != "" {
		grant.TokenType = fresh.TokenType
	}
	grant.TokenExpiresAt = nil
	if !fresh.Expiry.IsZero() {
		expiry := fresh.Expiry.UTC()
		grant.TokenExpiresAt = &expiry
	}
	if err := models.UpdateMCPOAuthGrantToken(tx, grant); err != nil {
		return fmt.Errorf("failed persisting refreshed oauth token: %w", err)
	}
	return nil
}

// mcpGrantNeedsRefresh reports whether the stored access token is expired or
// close enough to expiry that a session should not be started on it.
//
// A grant with no recorded expiry is treated as live: a provider that omits
// expires_in is promising exactly that, and refreshing on every session open
// would burn a rotating refresh token for nothing.
func mcpGrantNeedsRefresh(grant *models.MCPOAuthGrant, now time.Time) bool {
	if grant.TokenExpiresAt == nil {
		return false
	}
	return !now.Add(mcpGrantExpiryMargin).Before(*grant.TokenExpiresAt)
}

// mcpTokenClient builds the OAuth client config for token-endpoint calls
// against the authorization server that issued this grant.
//
// The endpoints are read off the grant rather than rediscovered: only the
// authorization server that minted a refresh token can renew it, and re-running
// RFC 8414 discovery on the session-open path adds a network hop that can only
// disagree with what the login used.
func mcpTokenClient(grant *models.MCPOAuthGrant, clientSecret string) oauth.ClientConfig {
	cfg := oauth.ClientConfig{
		ClientID:      grant.ClientID,
		ClientSecret:  clientSecret,
		TokenEndpoint: grant.TokenEndpoint,
		Audience:      grant.Resource,
		HTTPClient:    mcpOAuthHTTPClient,
	}
	if scopes := strings.Fields(grant.Scopes); len(scopes) > 0 {
		cfg.Scopes = scopes
	}
	return cfg
}

func authorizationHeader(tokenType, accessToken string) string {
	if tokenType == "" || strings.EqualFold(tokenType, "bearer") {
		tokenType = "Bearer"
	}
	return strings.TrimSpace(tokenType + " " + accessToken)
}

func decryptOptional(cipher []byte) (string, error) {
	if len(cipher) == 0 {
		return "", nil
	}
	return models.DecryptCredentialSecretKey(cipher)
}
