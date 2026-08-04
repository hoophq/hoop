// HTTP handlers for the MCP connection OAuth login flow.
//
// When an admin configures an MCP connection whose endpoint is protected by
// OAuth (e.g. https://mcp.figma.com/mcp), the connection setup page drives a
// browser OAuth login through these three endpoints:
//
//	POST /mcp-oauth/authorize      (admin)        -> { authorization_url, flow_id }
//	GET  /mcp-oauth/callback       (no auth)      -> redirects back to the app
//	GET  /mcp-oauth/token/{flowID} (admin)        -> { authorization_header, ... } (once)
//
// The login runs before the connection exists (the admin authorizes while
// filling the create form), so state is held in the short-lived
// private.mcp_oauth_flows table keyed by the OAuth state parameter rather than
// by connection. The callback is unauthenticated because the upstream provider
// redirects the browser to it directly; it is secured by the single-use,
// TTL-bounded state row created at authorize time, and only a pending flow may
// change state there — a replay of a settled one is answered with the outcome
// it already has rather than allowed to downgrade it.
//
// What happens to the token afterwards depends on the subtype. Both freeze it
// into HEADER_AUTHORIZATION, which is what the agent reads. The mcpproxy
// subtype additionally adopts the flow into a durable grant when the
// connection is saved (services.AdoptMCPOAuthGrant), so the credential is
// renewed from its refresh token at every session open instead of dying at the
// provider's TTL — but only when the flow authorized the server that
// connection actually points at. The legacy mcp subtype keeps the frozen
// header alone.
//
// The OAuth protocol itself lives in services/mcp_oauth_client.go, over
// mcpproxy's auth/outbound/oauth.
package apiconnections

import (
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
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// mcpOAuthFlowTTL bounds how long an MCP OAuth login may stay open between the
// authorize redirect and the upstream provider's callback. Generous enough for
// a human to complete a login screen, short enough to limit replay of a leaked
// state value.
const mcpOAuthFlowTTL = 10 * time.Minute

// mcpOAuthFlowRetention bounds how long a flow row survives regardless of what
// happened to it. A completed flow stays adoptable well past the login TTL
// because the admin still has a create form to finish, but not indefinitely:
// past this age the encrypted refresh token it holds is garbage nobody will
// claim.
const mcpOAuthFlowRetention = 24 * time.Hour

// mcpOAuthCallbackPath is the gateway-relative path the upstream authorization
// server redirects to. It is registered as the redirect URI during dynamic
// client registration, and must match the redirect URI an admin whitelists
// when providing client credentials manually.
const mcpOAuthCallbackPath = "/api/mcp-oauth/callback"

// mcpOAuthRedirectURI builds the absolute callback URL from the configured API
// URL. This is the value registered with the upstream authorization server.
func mcpOAuthRedirectURI() string {
	return appconfig.Get().FullApiURL() + mcpOAuthCallbackPath
}

// StartMCPOAuth
//
//	@Summary		Start the OAuth login flow for an MCP connection
//	@Description	Discovers the MCP server's authorization server (RFC 9728 / RFC 8414), optionally performs Dynamic Client Registration (RFC 7591) when no client credentials are supplied, and returns the authorization URL for the admin's browser to complete an Authorization Code + PKCE login. The browser is redirected there; the upstream provider redirects back to the gateway callback, which exchanges the code for a token. Used by the connection create page.
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.MCPOAuthAuthorizeRequest	true	"MCP OAuth login request"
//	@Success		200		{object}	openapi.MCPOAuthAuthorizeResponse
//	@Failure		400,422,500	{object}	openapi.HTTPError
//	@Router			/mcp-oauth/authorize [post]
func StartMCPOAuth(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if ctx.GetUserID() == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "an authenticated user is required to start the login flow"})
		return
	}

	var req openapi.MCPOAuthAuthorizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	req.ServerURL = strings.TrimSpace(req.ServerURL)
	if req.ServerURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "server_url is required"})
		return
	}

	// Sweep abandoned logins here rather than on a schedule. A flow nobody
	// polls is never consumed, and a completed one that no connection adopted
	// still holds an encrypted refresh token. The window is generous: a flow
	// stays adoptable long enough for an admin to finish the create form they
	// authorized from.
	if n, err := models.PurgeStaleMCPOAuthFlows(models.DB, mcpOAuthFlowRetention); err != nil {
		log.Warnf("mcp oauth: failed purging stale flows: %v", err)
	} else if n > 0 {
		log.Infof("mcp oauth: purged %d stale oauth flows", n)
	}

	redirectBack, err := parseFederationRedirect(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	discovery, err := services.DiscoverMCPAuthServer(c.Request.Context(), req.ServerURL)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	clientID := strings.TrimSpace(req.ClientID)
	clientSecret := strings.TrimSpace(req.ClientSecret)
	tokenAuthMethod := "none"
	switch {
	case clientID != "" && clientSecret != "":
		tokenAuthMethod = "client_secret_post"
	case clientID != "":
		tokenAuthMethod = "none"
	default:
		// No client credentials supplied: register dynamically.
		reg, err := services.RegisterMCPClient(c.Request.Context(), discovery.RegistrationEndpoint,
			mcpOAuthRedirectURI(), discovery.ScopesSupported)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
		clientID = reg.ClientID
		clientSecret = reg.ClientSecret
		tokenAuthMethod = reg.TokenAuthMethod
	}

	verifier, challenge, err := services.GenerateMCPPKCE()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed generating PKCE challenge: %v", err)
		return
	}

	scopes := strings.TrimSpace(req.Scopes)
	if scopes == "" {
		scopes = strings.Join(discovery.ScopesSupported, " ")
	}

	verifierCipher, err := models.EncryptCredentialSecretKey(verifier)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed encrypting PKCE verifier: %v", err)
		return
	}
	var clientSecretCipher []byte
	if clientSecret != "" {
		clientSecretCipher, err = models.EncryptCredentialSecretKey(clientSecret)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed encrypting client secret: %v", err)
			return
		}
	}

	flow := &models.MCPOAuthFlow{
		ID:                    uuid.NewString(),
		OrgID:                 ctx.GetOrgID(),
		UserID:                ctx.GetUserID(),
		ServerURL:             req.ServerURL,
		Resource:              discovery.Resource,
		Issuer:                discovery.Issuer,
		AuthorizationEndpoint: discovery.AuthorizationEndpoint,
		TokenEndpoint:         discovery.TokenEndpoint,
		ClientID:              clientID,
		ClientSecretEncrypted: clientSecretCipher,
		TokenAuthMethod:       tokenAuthMethod,
		CodeVerifierEncrypted: verifierCipher,
		Scopes:                scopes,
		RedirectURL:           redirectBack,
		Status:                models.MCPOAuthFlowStatusPending,
	}
	if err := models.CreateMCPOAuthFlow(models.DB, flow); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating oauth flow: %v", err)
		return
	}

	authURL, err := services.BuildMCPAuthorizationURL(discovery, clientID, mcpOAuthRedirectURI(), flow.ID, challenge, scopes)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed building authorization url: %v", err)
		return
	}

	c.JSON(http.StatusOK, openapi.MCPOAuthAuthorizeResponse{
		AuthorizationURL: authURL,
		FlowID:           flow.ID,
	})
}

// MCPOAuthCallback
//
//	@Summary			OAuth callback for MCP connection login
//	@Description		The upstream authorization server's redirect target. Validates the state issued by the authorize endpoint, exchanges the authorization code for an access token, stores it on the flow row, and redirects the browser back to the connection create page with mcp_oauth=success|error and the flow_id. This endpoint is unauthenticated but state-validated, and is not called directly by clients.
//	@Tags				Connections
//	@Param				state	query	string	false	"OAuth state (the flow id issued by the authorize endpoint)"
//	@Param				code	query	string	false	"OAuth authorization code"
//	@Param				error	query	string	false	"OAuth error returned by the authorization server"
//	@Success			307		"Redirect back to the application with mcp_oauth=success|error"
//	@Router				/mcp-oauth/callback [get]
func MCPOAuthCallback(c *gin.Context) {
	stateID := c.Query("state")
	if stateID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "missing state"})
		return
	}

	flow, err := models.GetMCPOAuthFlow(models.DB, stateID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"message": "unknown or already-consumed oauth state"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading oauth flow: %v", err)
		return
	}

	redirectBack := flow.RedirectURL
	if redirectBack == "" {
		redirectBack = appconfig.Get().FullApiURL() + "/"
	}

	// This endpoint is unauthenticated by design — the provider redirects the
	// browser here — so anyone holding the callback URL can replay it, and a
	// browser back button is enough. Only a pending flow may still change
	// state. Replaying one that already succeeded used to run the failure
	// path below (the code is spent, so the exchange fails), flipping a good
	// flow to error and destroying the credential a freshly created
	// connection was about to adopt — an unauthenticated request silently
	// breaking a connection an admin just saved. Answer the replay with the
	// outcome the flow already has and touch nothing.
	switch flow.Status {
	case models.MCPOAuthFlowStatusCompleted, models.MCPOAuthFlowStatusConsumed:
		log.With("flow-id", flow.ID).Infof("mcp oauth: ignoring callback replay for a %s flow", flow.Status)
		c.Redirect(http.StatusTemporaryRedirect, withMCPOutcome(redirectBack, "success", flow.ID, ""))
		return
	case models.MCPOAuthFlowStatusError:
		c.Redirect(http.StatusTemporaryRedirect, withMCPOutcome(redirectBack, "error", flow.ID, flow.ErrorReason))
		return
	}

	// A terminal failure marks the flow rather than deleting it: the create
	// page polls the token endpoint next and needs the reason to render. The
	// row is consumed there, and any flow nobody ever polls is swept by
	// PurgeStaleMCPOAuthFlows on the next authorize.
	failFlow := func(reason string) {
		flow.Status = models.MCPOAuthFlowStatusError
		flow.ErrorReason = reason
		if uerr := models.UpdateMCPOAuthFlowResult(models.DB, flow); uerr != nil {
			log.Warnf("mcp oauth: failed marking flow %s as error: %v", flow.ID, uerr)
		}
		c.Redirect(http.StatusTemporaryRedirect, withMCPOutcome(redirectBack, "error", flow.ID, reason))
	}

	if time.Since(flow.CreatedAt) > mcpOAuthFlowTTL {
		log.Warnf("mcp oauth: flow %s expired", flow.ID)
		failFlow("login_expired")
		return
	}
	if oauthErr := c.Query("error"); oauthErr != "" {
		log.Warnf("mcp oauth: login denied for flow %s: %s", flow.ID, oauthErr)
		failFlow("login_denied")
		return
	}
	code := c.Query("code")
	if code == "" {
		failFlow("missing_code")
		return
	}

	codeVerifier, err := models.DecryptCredentialSecretKey(flow.CodeVerifierEncrypted)
	if err != nil {
		log.Errorf("mcp oauth: failed decrypting verifier for flow %s: %v", flow.ID, err)
		failFlow("internal_error")
		return
	}
	clientSecret := ""
	if len(flow.ClientSecretEncrypted) > 0 {
		clientSecret, err = models.DecryptCredentialSecretKey(flow.ClientSecretEncrypted)
		if err != nil {
			log.Errorf("mcp oauth: failed decrypting client secret for flow %s: %v", flow.ID, err)
			failFlow("internal_error")
			return
		}
	}

	token, err := services.ExchangeMCPCode(c.Request.Context(),
		&services.MCPDiscovery{TokenEndpoint: flow.TokenEndpoint, Resource: flow.Resource},
		flow.ClientID, clientSecret, flow.TokenAuthMethod, code, mcpOAuthRedirectURI(), codeVerifier)
	if err != nil {
		log.Warnf("mcp oauth: code exchange failed for flow %s: %v", flow.ID, err)
		failFlow("exchange_failed")
		return
	}

	accessCipher, err := models.EncryptCredentialSecretKey(token.AccessToken)
	if err != nil {
		log.Errorf("mcp oauth: failed encrypting access token for flow %s: %v", flow.ID, err)
		failFlow("internal_error")
		return
	}
	var refreshCipher []byte
	if token.RefreshToken != "" {
		refreshCipher, err = models.EncryptCredentialSecretKey(token.RefreshToken)
		if err != nil {
			log.Errorf("mcp oauth: failed encrypting refresh token for flow %s: %v", flow.ID, err)
			failFlow("internal_error")
			return
		}
	}

	flow.Status = models.MCPOAuthFlowStatusCompleted
	flow.ErrorReason = ""
	flow.AccessTokenEncrypted = accessCipher
	flow.RefreshTokenEncrypted = refreshCipher
	flow.TokenType = token.TokenType
	if !token.Expiry.IsZero() {
		expiresAt := token.Expiry.UTC()
		flow.TokenExpiresAt = &expiresAt
	}
	if err := models.UpdateMCPOAuthFlowResult(models.DB, flow); err != nil {
		log.Errorf("mcp oauth: failed persisting token for flow %s: %v", flow.ID, err)
		failFlow("internal_error")
		return
	}

	log.With("flow-id", flow.ID, "server", flow.ServerURL).Infof("mcp oauth: login completed")
	c.Redirect(http.StatusTemporaryRedirect, withMCPOutcome(redirectBack, "success", flow.ID, ""))
}

// GetMCPOAuthToken
//
//	@Summary		Retrieve the token obtained by an MCP OAuth login
//	@Description	Returns the access token obtained by a completed MCP OAuth login so the connection create page can freeze it into the connection's HEADER_AUTHORIZATION configuration. The token is returned at most once: the flow row is deleted on read.
//	@Tags			Connections
//	@Produce		json
//	@Param			flowID	path		string	true	"The flow id returned by the authorize endpoint"
//	@Success		200		{object}	openapi.MCPOAuthTokenResponse
//	@Failure		404,409,425,500	{object}	openapi.HTTPError
//	@Router			/mcp-oauth/token/{flowID} [get]
func GetMCPOAuthToken(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	flowID := c.Param("flowID")

	flow, err := models.GetMCPOAuthFlow(models.DB, flowID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "oauth flow not found or already consumed"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading oauth flow: %v", err)
		return
	}
	// Scope the flow to the org that started it. A flow id is a random UUID, but
	// this prevents a leaked id from being redeemed across orgs.
	if flow.OrgID != ctx.GetOrgID() {
		c.JSON(http.StatusNotFound, gin.H{"message": "oauth flow not found or already consumed"})
		return
	}

	switch flow.Status {
	case models.MCPOAuthFlowStatusCompleted:
		// proceed
	case models.MCPOAuthFlowStatusConsumed:
		// The token already left the gateway and the access token was
		// scrubbed, so there is nothing left to return. Re-reading is a
		// double submit, not a new login.
		c.JSON(http.StatusConflict, gin.H{"message": "oauth token has already been redeemed"})
		return
	case models.MCPOAuthFlowStatusError:
		reason := flow.ErrorReason
		_ = models.DeleteMCPOAuthFlow(models.DB, flow.ID)
		c.JSON(http.StatusConflict, gin.H{"message": "oauth login failed", "reason": reason})
		return
	default:
		c.JSON(http.StatusTooEarly, gin.H{"message": "oauth login has not completed yet"})
		return
	}

	accessToken, err := models.DecryptCredentialSecretKey(flow.AccessTokenEncrypted)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed decrypting access token: %v", err)
		return
	}
	refreshToken := ""
	if len(flow.RefreshTokenEncrypted) > 0 {
		if refreshToken, err = models.DecryptCredentialSecretKey(flow.RefreshTokenEncrypted); err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed decrypting refresh token: %v", err)
			return
		}
	}

	tokenType := flow.TokenType
	if tokenType == "" || tokenType == "bearer" {
		tokenType = "Bearer"
	}
	var expiresIn int64
	if flow.TokenExpiresAt != nil {
		if d := time.Until(*flow.TokenExpiresAt); d > 0 {
			expiresIn = int64(d.Seconds())
		}
	}

	// The token is single use to the browser: mark the flow consumed and
	// scrub the access token now that it has left the gateway. The row itself
	// survives so the connection this login authorized can adopt its refresh
	// token when it is saved — the connection does not exist yet, so this is
	// the only place the two halves can be joined later.
	if derr := models.ConsumeMCPOAuthFlow(models.DB, flow.ID); derr != nil {
		log.Warnf("mcp oauth: failed consuming flow %s: %v", flow.ID, derr)
	}

	c.JSON(http.StatusOK, openapi.MCPOAuthTokenResponse{
		AccessToken:         accessToken,
		TokenType:           tokenType,
		AuthorizationHeader: strings.TrimSpace(tokenType + " " + accessToken),
		RefreshToken:        refreshToken,
		ExpiresIn:           expiresIn,
		ClientID:            flow.ClientID,
		ServerURL:           flow.ServerURL,
	})
}

// withMCPOutcome appends the login outcome to the app redirect URL so the
// create page can finalize (success) or surface an error toast. flow_id lets
// the page fetch the token; reason is included only for error outcomes.
func withMCPOutcome(redirectURL, outcome, flowID, reason string) string {
	u, err := url.Parse(redirectURL)
	if err != nil || u == nil {
		return redirectURL
	}
	q := u.Query()
	q.Set("mcp_oauth", outcome)
	q.Set("flow_id", flowID)
	if reason != "" {
		q.Set("reason", reason)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// AdoptMCPOAuthGrant promotes a completed OAuth login into a durable grant for
// a freshly saved connection, and reports why it could not when it did not.
//
// Called after create and update, and a no-op (empty warning) unless the
// payload carried a flow id for the mcpproxy subtype. Only that subtype gets
// renewal: the legacy mcp subtype runs the byte-relay path, which has no
// notion of a grant.
//
// Exported because the resource wizard creates its roles through
// POST /resources, in a different package, and a connection created there is
// no less in need of a renewable credential than one created through
// POST /connections.
//
// A failure does not fail the save. The connection row is already written and
// its frozen HEADER_AUTHORIZATION works exactly as it did before grants
// existed, so refusing here would turn a degraded credential lifecycle into a
// broken create flow. It is not silent either: the reason goes back on the
// response, because the alternative is a 201 for a connection that quietly
// stops working at the provider's token TTL — the admin finds out from a
// failing session days later, with nothing pointing at the save that caused
// it. The endpoint-mismatch refusal in services.AdoptMCPOAuthGrant is the case
// that most needs saying out loud: it means the admin authorized one server
// and saved another.
func AdoptMCPOAuthGrant(orgID string, conn *models.Connection, subType, flowID string) (warning string) {
	if flowID == "" || subType != services.MCPOAuthGrantSubType {
		return ""
	}
	if err := services.AdoptMCPOAuthGrant(orgID, conn.ID, flowID, conn.Envs); err != nil {
		log.With("connection-id", conn.ID, "flow-id", flowID).
			Warnf("mcp oauth: failed adopting grant, the connection keeps its frozen token: %v", err)
		return fmt.Sprintf("the OAuth login was not attached to this connection, so its credential will not be renewed "+
			"and the connection stops working when the provider expires the current token: %v", err)
	}
	log.With("connection-id", conn.ID, "flow-id", flowID).Infof("mcp oauth: grant adopted")
	return ""
}

// hasMCPOAuthGrant reports whether a durable grant backs this connection's
// credential.
//
// The edit screen needs it to render the right auth control, and nothing in
// the connection's env vars can tell it: a brokered OAuth login and a pasted
// bearer token are both one HEADER_AUTHORIZATION, and MCP_AUTH collapses
// "oauth" to "static" on purpose (the agent has no separate oauth mode — see
// validateMCPProxyEnv). Guessing "static" is what makes an OAuth connection
// reopen offering to replace a token instead of to re-authorize.
//
// Presence only. A lookup failure reads as "no grant": the caller renders a
// re-authorize affordance either way, and the alternative is failing a
// connection read over a credential detail.
func hasMCPOAuthGrant(orgID string, conn *models.Connection) bool {
	if conn == nil || conn.SubType.String != services.MCPOAuthGrantSubType {
		return false
	}
	grant, err := models.GetMCPOAuthGrant(models.DB, orgID, conn.ID, "")
	if err != nil {
		if !errors.Is(err, models.ErrNotFound) {
			log.With("connection-id", conn.ID).Warnf("mcp oauth: failed reading grant: %v", err)
		}
		return false
	}
	return grant != nil
}
