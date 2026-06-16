// OAuth consent flow for the gcp_oauth federation provider.
//
// gcp_oauth mints session tokens from a per-user Google OAuth refresh token
// rather than a service-account key. These endpoints let each end user grant
// (and revoke) that refresh token for a specific connection:
//
//   - GET    /connections/{id}/federation/oauth/authorize  (authed user)
//     Returns the Google consent URL. The browser is sent there; Google
//     redirects back to the callback below after the user approves.
//
//   - GET    /federation/oauth/callback                    (no auth; state-validated)
//     Google's redirect target. Exchanges the authorization code for a refresh
//     token, records the consented Google identity, encrypts and stores the
//     refresh token keyed by (connection, user), then redirects the browser
//     back to the app.
//
//   - DELETE /connections/{id}/federation/oauth            (authed user)
//     Disconnects the user's Google account for the connection (deletes the
//     stored refresh token).
//
// Unlike the admin-only federation config endpoints, authorize/disconnect are
// available to any authenticated user: each user manages their own credential.
// The callback is unauthenticated because Google calls it directly; it is
// secured by the single-use, TTL-bounded state row created at authorize time.
package apiconnections

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// federationOAuthStateTTL bounds how long a consent flow may stay open between
// the authorize redirect and Google's callback. Generous enough for a human to
// complete Google's consent screen, short enough to limit replay of a leaked
// state value.
const federationOAuthStateTTL = 10 * time.Minute

// federationOAuthClientConfig mirrors the gcpoauth resolver's client-config
// shape (the OAuth app credentials stored in AdminCredentialsEncrypted).
type federationOAuthClientConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// federationOAuthExtraConfig is the subset of ExtraConfig the consent flow
// reads. scopes is optional; cloud-platform is requested by default.
type federationOAuthExtraConfig struct {
	Scopes []string `json:"scopes"`
}

// federationOAuthCallbackPath is the gateway-relative path Google redirects to.
// It must match an Authorized redirect URI registered on the OAuth client in
// the GCP console.
const federationOAuthCallbackPath = "/api/federation/oauth/callback"

// federationOAuthRedirectURI builds the absolute callback URL from the
// configured API URL. This is the value that must be whitelisted in the GCP
// OAuth client.
func federationOAuthRedirectURI() string {
	return appconfig.Get().FullApiURL() + federationOAuthCallbackPath
}

// AuthorizeFederationOAuth
//
//	@Summary		Start the Google OAuth consent flow for a connection
//	@Description	Returns the Google consent URL for the authenticated user to connect their Google account to a gcp_oauth-federated connection. The browser should be redirected to the returned URL; Google redirects back to the gateway callback, which stores the resulting refresh token.
//	@Tags			Federation
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the connection"
//	@Param			redirect	query		string	false	"URL to return the browser to after consent (must match the API hostname)"
//	@Success		200			{object}	openapi.FederationOAuthAuthorizeResponse
//	@Failure		400,404,409,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/federation/oauth/authorize [get]
func AuthorizeFederationOAuth(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if ctx.GetUserID() == "" || ctx.UserEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "an authenticated user is required to start the consent flow"})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching connection: %v", err)
		return
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	cfg, err := models.GetConnectionFederationConfig(models.DB, ctx.GetOrgID(), conn.ID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "federation not configured for this connection"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching federation config: %v", err)
		return
	}
	if cfg.BuiltinProvider == nil || *cfg.BuiltinProvider != models.FederationProviderGCPOAuth {
		c.JSON(http.StatusConflict, gin.H{"message": "the OAuth consent flow is only available for gcp_oauth federation"})
		return
	}

	clientCfg, err := decodeFederationOAuthClient(cfg.AdminCredentialsEncrypted)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading oauth client config: %v", err)
		return
	}

	redirectBack, err := parseFederationRedirect(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	state := &models.FederationOAuthState{
		ID:           uuid.NewString(),
		OrgID:        ctx.GetOrgID(),
		ConnectionID: conn.ID,
		UserID:       ctx.GetUserID(),
		UserEmail:    ctx.UserEmail,
		RedirectURL:  redirectBack,
	}
	if err := models.CreateFederationOAuthState(models.DB, state); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating oauth state: %v", err)
		return
	}

	conf := federationOAuthConfig(clientCfg, scopesFromConfig(cfg.ExtraConfig))
	// AccessTypeOffline + prompt=consent forces Google to return a refresh
	// token on every consent, even for a returning user. Without both, a
	// second authorization returns only an access token and we would have no
	// refresh token to persist.
	authURL := conf.AuthCodeURL(state.ID,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
	c.JSON(http.StatusOK, openapi.FederationOAuthAuthorizeResponse{URL: authURL})
}

// FederationOAuthCallback
//
//	@Summary				Google OAuth callback for connection federation
//	@Description.markdown	api-federation-oauth-callback
//	@Tags					Federation
//	@Param					state	query	string	false	"OAuth state (issued by the authorize endpoint)"
//	@Param					code	query	string	false	"OAuth authorization code"
//	@Param					error	query	string	false	"OAuth error returned by Google"
//	@Success				307		"Redirect back to the application with federation_oauth=success|error"
//	@Router					/federation/oauth/callback [get]
func FederationOAuthCallback(c *gin.Context) {
	stateID := c.Query("state")
	if stateID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "missing state"})
		return
	}

	state, err := models.GetFederationOAuthState(models.DB, stateID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"message": "unknown or already-consumed oauth state"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading oauth state: %v", err)
		return
	}
	// A state is single use: consume it regardless of outcome.
	defer func() {
		if derr := models.DeleteFederationOAuthState(models.DB, state.ID); derr != nil {
			log.Warnf("federation oauth: failed deleting consumed state %s: %v", state.ID, derr)
		}
	}()

	redirectBack := state.RedirectURL
	if redirectBack == "" {
		redirectBack = appconfig.Get().FullApiURL() + "/"
	}

	if time.Since(state.CreatedAt) > federationOAuthStateTTL {
		log.Warnf("federation oauth: state %s expired", state.ID)
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "consent_expired"))
		return
	}

	if oauthErr := c.Query("error"); oauthErr != "" {
		log.Warnf("federation oauth: consent denied for user=%s connection=%s: %s", state.UserEmail, state.ConnectionID, oauthErr)
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "consent_denied"))
		return
	}

	code := c.Query("code")
	if code == "" {
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "missing_code"))
		return
	}

	cfg, err := models.GetConnectionFederationConfig(models.DB, state.OrgID, state.ConnectionID)
	if err != nil {
		log.Errorf("federation oauth: failed loading config for connection %s: %v", state.ConnectionID, err)
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "config_unavailable"))
		return
	}
	if cfg.BuiltinProvider == nil || *cfg.BuiltinProvider != models.FederationProviderGCPOAuth {
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "provider_mismatch"))
		return
	}
	clientCfg, err := decodeFederationOAuthClient(cfg.AdminCredentialsEncrypted)
	if err != nil {
		log.Errorf("federation oauth: failed decoding client config for connection %s: %v", state.ConnectionID, err)
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "config_unavailable"))
		return
	}

	exchangeCtx, cancel := context.WithTimeout(c.Request.Context(), federationTestTimeout)
	defer cancel()

	conf := federationOAuthConfig(clientCfg, scopesFromConfig(cfg.ExtraConfig))
	token, err := conf.Exchange(exchangeCtx, code)
	if err != nil {
		log.Warnf("federation oauth: code exchange failed for user=%s: %v", state.UserEmail, err)
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "exchange_failed"))
		return
	}
	if token.RefreshToken == "" {
		// No refresh token means we cannot mint future session tokens. This
		// happens when Google withholds it (consent not forced); the
		// AccessTypeOffline + prompt=consent params above are meant to prevent
		// this, so surface it clearly rather than storing an unusable record.
		log.Warnf("federation oauth: no refresh token returned for user=%s (consent screen may need prompt=consent)", state.UserEmail)
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "no_refresh_token"))
		return
	}

	googleEmail := emailFromIDToken(token)

	ciphertext, err := models.EncryptCredentialSecretKey(token.RefreshToken)
	if err != nil {
		log.Errorf("federation oauth: failed encrypting refresh token for user=%s: %v", state.UserEmail, err)
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "internal_error"))
		return
	}

	cred := &models.FederationUserCredential{
		ID:                    uuid.NewString(),
		OrgID:                 state.OrgID,
		ConnectionID:          state.ConnectionID,
		UserID:                state.UserID,
		UserEmail:             state.UserEmail,
		GoogleEmail:           googleEmail,
		RefreshTokenEncrypted: ciphertext,
		Scopes:                scopesString(scopesFromConfig(cfg.ExtraConfig)),
	}
	if err := models.UpsertFederationUserCredential(models.DB, cred); err != nil {
		log.Errorf("federation oauth: failed persisting credential for user=%s: %v", state.UserEmail, err)
		c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "error", "internal_error"))
		return
	}

	log.With("connection-id", state.ConnectionID, "user", state.UserEmail, "google-email", googleEmail).
		Infof("federation oauth: stored user refresh token")
	c.Redirect(http.StatusTemporaryRedirect, withFederationOutcome(redirectBack, "success", ""))
}

// DisconnectFederationOAuth
//
//	@Summary		Disconnect a user's Google account from a connection
//	@Description	Deletes the authenticated user's stored Google refresh token for a gcp_oauth-federated connection. Subsequent sessions fail until the user re-consents.
//	@Tags			Federation
//	@Param			nameOrID	path	string	true	"Name or UUID of the connection"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/federation/oauth [delete]
func DisconnectFederationOAuth(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	conn, err := models.GetConnectionByNameOrID(ctx, c.Param("nameOrID"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching connection: %v", err)
		return
	}
	if conn == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return
	}

	if err := models.DeleteFederationUserCredential(models.DB, ctx.GetOrgID(), conn.ID, ctx.GetUserID()); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "no connected Google account for this connection"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed disconnecting Google account: %v", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// federationOAuthConfig builds the oauth2.Config used by both the authorize
// and callback handlers. openid+email are always requested so the callback can
// read the consented Google identity from the returned id_token; the
// data-plane scopes (cloud-platform by default) come from the connection's
// extra_config.
func federationOAuthConfig(client federationOAuthClientConfig, dataScopes []string) *oauth2.Config {
	scopes := append([]string{"openid", "email"}, dataScopes...)
	return &oauth2.Config{
		ClientID:     client.ClientID,
		ClientSecret: client.ClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  federationOAuthRedirectURI(),
		Scopes:       scopes,
	}
}

func scopesFromConfig(raw json.RawMessage) []string {
	var extra federationOAuthExtraConfig
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &extra)
	}
	if len(extra.Scopes) == 0 {
		return []string{"https://www.googleapis.com/auth/cloud-platform"}
	}
	return extra.Scopes
}

func scopesString(scopes []string) string {
	return strings.Join(scopes, " ")
}

// decodeFederationOAuthClient decrypts and parses the OAuth client config blob
// stored in the federation row's admin credentials column.
func decodeFederationOAuthClient(ciphertext []byte) (federationOAuthClientConfig, error) {
	var client federationOAuthClientConfig
	if len(ciphertext) == 0 {
		return client, fmt.Errorf("oauth client credentials are not configured for this connection")
	}
	plain, err := models.DecryptCredentialSecretKey(ciphertext)
	if err != nil {
		return client, fmt.Errorf("failed decrypting oauth client credentials: %w", err)
	}
	if err := json.Unmarshal([]byte(plain), &client); err != nil {
		return client, fmt.Errorf("oauth client credentials are not valid JSON: %w", err)
	}
	if client.ClientID == "" || client.ClientSecret == "" {
		return client, fmt.Errorf("oauth client credentials must contain client_id and client_secret")
	}
	return client, nil
}

// emailFromIDToken extracts the email claim from the id_token returned by
// Google's token endpoint. The id_token is obtained directly from Google over
// TLS during the code exchange (it is not relayed through the user's browser),
// so reading the claim from the payload segment is sufficient here; we do not
// re-verify the signature. Returns an empty string when no id_token/email is
// present (the caller stores it as-is — the credential is still usable, the
// audit principal is simply unknown).
func emailFromIDToken(token *oauth2.Token) string {
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return ""
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Email
}

// parseFederationRedirect validates the optional redirect query param against
// the API hostname (same policy as the OIDC login flow) and falls back to the
// app root. This prevents the callback from being used as an open redirector.
func parseFederationRedirect(c *gin.Context) (string, error) {
	redirectURL := c.Query("redirect")
	if redirectURL == "" {
		return appconfig.Get().FullApiURL() + "/", nil
	}
	u, err := url.Parse(redirectURL)
	if err != nil || u == nil || u.Hostname() != appconfig.Get().ApiHostname() {
		return "", fmt.Errorf("redirect attribute does not match with api url")
	}
	return redirectURL, nil
}

// withFederationOutcome appends the consent outcome to the app redirect URL so
// the frontend can render a success/error toast. reason is included only for
// error outcomes to aid debugging.
func withFederationOutcome(redirectURL, outcome, reason string) string {
	u, err := url.Parse(redirectURL)
	if err != nil || u == nil {
		return redirectURL
	}
	q := u.Query()
	q.Set("federation_oauth", outcome)
	if reason != "" {
		q.Set("reason", reason)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
