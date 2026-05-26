// Federation endpoints for connection-level IAM federation. All endpoints
// require AdminOnlyAccessRole — federation configuration is sensitive
// (admin SA JSON ciphertext lives in the row) and end users have no business
// reading or editing it.
//
// Wire shape and security choices:
//
//   - GET returns a redacted view: admin_credentials_json is never echoed
//     back, only has_admin_credentials. This lets the admin UI render
//     "credentials configured ✅" without ever holding the secret in JS.
//
//   - PUT is upsert. Omitting admin_credentials_json on an update preserves
//     the stored ciphertext; sending a new value re-encrypts. Sending an
//     empty string ("") clears the credential — explicit and auditable.
//
//   - POST /test runs a dry-run: it executes the resolver against a synthetic
//     user (admin-supplied email) and returns the *shape* of what would be
//     injected (env-var keys only, never values). Used by the admin UI to
//     validate config without opening a real session.
package apiconnections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const federationTestTimeout = 30 * time.Second

// GetConnectionFederationConfig
//
//	@Summary		Get Federation Configuration for a Connection
//	@Description	Returns the IAM federation configuration for a connection. The admin credentials are never returned in plaintext; only a presence indicator is included.
//	@Tags			Connections
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the connection"
//	@Success		200			{object}	openapi.ConnectionFederationConfig
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/federation [get]
func GetConnectionFederationConfig(c *gin.Context) {
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

	cfg, err := models.GetConnectionFederationConfig(models.DB, ctx.GetOrgID(), conn.ID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "federation not configured for this connection"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching federation config: %v", err)
		return
	}

	c.JSON(http.StatusOK, modelToAPI(cfg))
}

// PutConnectionFederationConfig
//
//	@Summary		Upsert Federation Configuration for a Connection
//	@Description	Creates or updates the IAM federation configuration for a connection. AdminCredentialsJSON is write-only; omit on update to preserve the stored value.
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string								true	"Name or UUID of the connection"
//	@Param			request		body		openapi.ConnectionFederationConfig	true	"The request body resource"
//	@Success		200			{object}	openapi.ConnectionFederationConfig
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/federation [put]
func PutConnectionFederationConfig(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.ConnectionFederationConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
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

	if err := validateFederationRequest(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existing, getErr := models.GetConnectionFederationConfig(models.DB, ctx.GetOrgID(), conn.ID)
	if getErr != nil && !errors.Is(getErr, models.ErrNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, getErr, "failed fetching existing federation config: %v", getErr)
		return
	}

	cfg := apiToModel(req, ctx.GetOrgID(), conn.ID)

	// Credential lifecycle:
	// - non-empty new value  → encrypt + store fresh.
	// - omitted (nil pointer semantics: empty string in JSON is omitted by
	//   omitempty on the request, so we treat empty == "no change") → keep
	//   existing ciphertext.
	// - explicit clearing requires the dedicated DELETE endpoint; PUT never
	//   wipes credentials silently.
	if req.AdminCredentialsJSON != "" {
		ciphertext, encErr := models.EncryptCredentialSecretKey(req.AdminCredentialsJSON)
		if encErr != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, encErr, "failed encrypting admin credentials: %v", encErr)
			return
		}
		cfg.AdminCredentialsEncrypted = ciphertext
	} else if existing != nil {
		cfg.AdminCredentialsEncrypted = existing.AdminCredentialsEncrypted
	}

	if existing != nil {
		cfg.ID = existing.ID
		cfg.CreatedAt = existing.CreatedAt
	} else {
		cfg.ID = uuid.NewString()
	}

	if err := models.UpsertConnectionFederationConfig(models.DB, cfg); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed saving federation config: %v", err)
		return
	}

	// Re-read so timestamps and any DB-default field reflect what's on disk.
	saved, err := models.GetConnectionFederationConfig(models.DB, ctx.GetOrgID(), conn.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed re-fetching federation config: %v", err)
		return
	}
	c.JSON(http.StatusOK, modelToAPI(saved))
}

// DeleteConnectionFederationConfig
//
//	@Summary		Delete Federation Configuration for a Connection
//	@Description	Removes the IAM federation configuration for a connection. Subsequent sessions on this connection revert to the static connection envs.
//	@Tags			Connections
//	@Param			nameOrID	path	string	true	"Name or UUID of the connection"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/federation [delete]
func DeleteConnectionFederationConfig(c *gin.Context) {
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

	if err := models.DeleteConnectionFederationConfig(models.DB, ctx.GetOrgID(), conn.ID); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "federation not configured for this connection"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting federation config: %v", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// TestConnectionFederationConfig
//
//	@Summary		Dry-Run a Federation Resolution
//	@Description	Executes the configured federation resolver against a synthetic user without opening a session. Returns the resolved principal and the env-var keys that would be injected; secret values are never returned.
//	@Tags			Connections
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string							true	"Name or UUID of the connection"
//	@Param			request		body		openapi.FederationTestRequest	true	"The request body resource"
//	@Success		200			{object}	openapi.FederationTestResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/connections/{nameOrID}/federation/test [post]
func TestConnectionFederationConfig(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.FederationTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.UserEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "user_email is required"})
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

	userID := req.UserID
	if userID == "" {
		// Stable synthetic ID so repeated dry-runs trace to the same actor
		// in resolver-side logs.
		userID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("federation-test:"+req.UserEmail)).String()
	}

	dryCtx, cancel := context.WithTimeout(context.Background(), federationTestTimeout)
	defer cancel()

	res, resolveErr := services.ResolveFederation(dryCtx, cfg, services.FederationInput{
		OrgID:        ctx.GetOrgID(),
		ConnectionID: conn.ID,
		AgentID:      conn.AgentID.String,
		SessionID:    "federation-dry-run-" + userID,
		UserID:       userID,
		UserEmail:    req.UserEmail,
	})
	if resolveErr != nil {
		c.JSON(http.StatusOK, openapi.FederationTestResponse{
			Success: false,
			Error:   resolveErr.Error(),
		})
		return
	}

	keys := make([]string, 0, len(res.EnvVars))
	for k := range res.EnvVars {
		keys = append(keys, k)
	}
	c.JSON(http.StatusOK, openapi.FederationTestResponse{
		Success:           true,
		ResolvedPrincipal: res.ResolvedPrincipal,
		AdminPrincipal:    res.AdminPrincipal,
		EnvVarKeys:        keys,
		TokenExpiresAt:    res.TokenExpiresAt.Format(time.RFC3339),
	})
}

// validateFederationRequest enforces the same shape constraints as the
// database CHECK clauses, surfacing them as 400s rather than 500s so the UI
// can render actionable error messages.
func validateFederationRequest(req openapi.ConnectionFederationConfig) error {
	switch req.HookSource {
	case models.FederationHookSourceBuiltin:
		if req.BuiltinProvider == "" {
			return errBadRequest("builtin_provider is required when hook_source=builtin")
		}
		// Whitelist the known providers; the DB CHECK is open but we want
		// the API to reject typos early.
		switch req.BuiltinProvider {
		case models.FederationProviderGCPIAM:
		default:
			return errBadRequest("unknown builtin_provider %q", req.BuiltinProvider)
		}
	default:
		return errBadRequest("hook_source must be %q", models.FederationHookSourceBuiltin)
	}
	switch req.FallbackPolicy {
	case "", models.FederationFallbackDeny:
	case models.FederationFallbackReadonly:
		if req.ReadonlyPrincipal == "" {
			return errBadRequest("readonly_principal is required when fallback_policy=readonly")
		}
	default:
		return errBadRequest("fallback_policy must be one of: deny, readonly")
	}
	if req.TokenTTLSeconds < 0 || req.TokenTTLSeconds > 43200 {
		return errBadRequest("token_ttl_seconds must be between 1 and 43200")
	}
	return nil
}

// errBadRequest returns a plain error suitable for direct surfacing in a 400.
// The handler wraps it in {"message": err.Error()} for consistency with the
// rest of the API.
func errBadRequest(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// modelToAPI projects the persisted row to the wire shape, hiding ciphertext.
func modelToAPI(cfg *models.ConnectionFederationConfig) openapi.ConnectionFederationConfig {
	out := openapi.ConnectionFederationConfig{
		ID:                      cfg.ID,
		ConnectionID:            cfg.ConnectionID,
		HookSource:              cfg.HookSource,
		IdentitySourceAttribute: cfg.IdentitySourceAttribute,
		IdentityTargetTemplate:  cfg.IdentityTargetTemplate,
		FallbackPolicy:          cfg.FallbackPolicy,
		TokenTTLSeconds:         cfg.TokenTTLSeconds,
		HasAdminCredentials:     len(cfg.AdminCredentialsEncrypted) > 0,
		CreatedAt:               cfg.CreatedAt.Format(time.RFC3339),
		UpdatedAt:               cfg.UpdatedAt.Format(time.RFC3339),
	}
	if cfg.BuiltinProvider != nil {
		out.BuiltinProvider = *cfg.BuiltinProvider
	}
	if cfg.ReadonlyPrincipal != nil {
		out.ReadonlyPrincipal = *cfg.ReadonlyPrincipal
	}
	if len(cfg.ExtraConfig) > 0 {
		extra := map[string]any{}
		if err := json.Unmarshal(cfg.ExtraConfig, &extra); err == nil {
			out.ExtraConfig = extra
		}
	}
	return out
}

// apiToModel translates the wire shape into a persistence-ready model.
// Defaults match the DB defaults so a partial PUT body still produces a
// valid row (e.g. omitted identity_source_attribute → "$.user.email").
func apiToModel(req openapi.ConnectionFederationConfig, orgID, connectionID string) *models.ConnectionFederationConfig {
	cfg := &models.ConnectionFederationConfig{
		OrgID:                   orgID,
		ConnectionID:            connectionID,
		HookSource:              req.HookSource,
		IdentitySourceAttribute: req.IdentitySourceAttribute,
		IdentityTargetTemplate:  req.IdentityTargetTemplate,
		FallbackPolicy:          req.FallbackPolicy,
		TokenTTLSeconds:         req.TokenTTLSeconds,
	}
	if cfg.IdentitySourceAttribute == "" {
		cfg.IdentitySourceAttribute = "$.user.email"
	}
	if cfg.IdentityTargetTemplate == "" {
		cfg.IdentityTargetTemplate = "{user.email}"
	}
	if cfg.FallbackPolicy == "" {
		cfg.FallbackPolicy = models.FederationFallbackDeny
	}
	if cfg.TokenTTLSeconds == 0 {
		cfg.TokenTTLSeconds = 3600
	}
	if req.BuiltinProvider != "" {
		v := req.BuiltinProvider
		cfg.BuiltinProvider = &v
	}
	if req.ReadonlyPrincipal != "" {
		v := req.ReadonlyPrincipal
		cfg.ReadonlyPrincipal = &v
	}
	if len(req.ExtraConfig) > 0 {
		raw, _ := json.Marshal(req.ExtraConfig)
		cfg.ExtraConfig = raw
	}
	return cfg
}
