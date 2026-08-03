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
//     saved, since no connection row exists while the admin is authorizing).
//   - ResolveMCPOAuthHeader renews the access token at session open and hands
//     back the Authorization header value for that session.
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
	"errors"
	"fmt"
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
func AdoptMCPOAuthGrant(orgID, connectionID, flowID string) error {
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
func ResolveMCPOAuthHeader(ctx context.Context, orgID, connectionID string) (string, error) {
	var header string
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		grant, err := models.LockMCPOAuthGrant(tx, orgID, connectionID, "")
		if err != nil {
			return err
		}
		header, err = resolveGrantHeader(ctx, tx, grant)
		return err
	})
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
			// The refresh token is dead. Drop the grant so the next session
			// reports a clean "not authorized" instead of retrying a rejected
			// credential every time it opens.
			if derr := models.DeleteMCPOAuthGrant(tx, grant.ID); derr != nil {
				log.Warnf("mcp oauth: failed deleting rejected grant %s: %v", grant.ID, derr)
			}
			return "", fmt.Errorf("the authorization server rejected the stored credential; re-authorize the connection: %w", err)
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
