// Federation endpoints for IAM federation. All endpoints require
// AdminOnlyAccessRole: federation configuration is sensitive (admin SA JSON
// ciphertext lives in the persisted row) and end users have no business
// reading or editing it.
//
// Wire shape and security choices:
//
//   - GET /connections/{id}/federation returns a redacted view:
//     admin_credentials_json is never echoed back, only
//     has_admin_credentials. This lets the admin UI render "credentials
//     configured" without ever holding the secret in JS.
//
//   - PUT /connections/{id}/federation is upsert. Omitting
//     admin_credentials_json on an update preserves the stored ciphertext;
//     sending a new value re-encrypts. Sending an empty string ("") clears
//     the credential (explicit and auditable).
//
//   - DELETE /connections/{id}/federation removes the row. Subsequent
//     sessions on this connection revert to the static connection envs.
//
//   - POST /federation/test is stateless and end-to-end. The body carries
//     a full candidate federation config AND a candidate connection
//     (agent_id, command, test_script, envs). The endpoint resolves the
//     federation against a synthetic user, merges the federated env vars
//     with the connection's static envs, and dispatches a one-shot
//     BareExec smoke probe to the named agent. The agent must be online.
//     The response carries the resolved principal, the env-var keys that
//     were injected (values never), and the probe's stdout+stderr — so
//     the admin UI can show a wizard user "your draft works end-to-end"
//     before any row is persisted.
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
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/federation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	transportsystem "github.com/hoophq/hoop/gateway/transport/system"
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

// TestFederationConfig
//
//	@Summary		Test a Federation Configuration End-to-End
//	@Description	Resolves the candidate federation configuration against a synthetic user AND dispatches a one-shot smoke probe (the caller-supplied test_script, e.g. "SELECT 1") to the agent identified in the request body. Persisted state is never read or written: the entire candidate connection + federation pair lives in the body. Returns the resolved principal, the env-var keys that were injected (values are never returned), and the agent-side stdout/stderr of the probe. Success requires both phases to succeed.
//	@Tags			Federation
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.FederationTestRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.FederationTestResponse
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/federation/test [post]
func TestFederationConfig(c *gin.Context) {
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
	if req.Config == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "config is required"})
		return
	}
	if req.Connection == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection is required"})
		return
	}
	if vErr := validateFederationRequest(*req.Config); vErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": vErr.Error()})
		return
	}
	if vErr := validateProbeConnection(*req.Connection); vErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": vErr.Error()})
		return
	}

	// ConnectionID is left empty: the resolver does not read it and the
	// endpoint is connection-agnostic by design (no DB lookup).
	cfg := apiToModel(*req.Config, ctx.GetOrgID(), "")

	// AdminCredentialsJSON is write-only on the wire; treat it as the
	// plaintext source for the resolver. Empty is allowed at this layer:
	// the provider itself will reject with a clear "missing admin
	// service-account credentials" message which we surface as
	// Success=false rather than a 400 (resolver-time concern, not a
	// config-shape concern).
	var adminPlain []byte
	if req.Config.AdminCredentialsJSON != "" {
		adminPlain = []byte(req.Config.AdminCredentialsJSON)
	}
	// Best-effort zero of the plaintext on return so the buffer is not
	// left lying around post-resolve. Go's GC may have already copied the
	// slice, but it costs nothing to clear what we own.
	defer func() {
		for i := range adminPlain {
			adminPlain[i] = 0
		}
	}()

	userID := req.UserID
	if userID == "" {
		// Stable synthetic ID so repeated dry-runs trace to the same actor
		// in resolver-side logs.
		userID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("federation-test:"+req.UserEmail)).String()
	}

	dryCtx, cancel := context.WithTimeout(context.Background(), federationTestTimeout)
	defer cancel()

	// Phase 1 — federation resolve. On failure the probe is skipped (no
	// federated env vars exist yet) and we return immediately with the
	// resolver's verbatim error.
	res, resolveErr := services.ResolveFederationDryRun(dryCtx, cfg, adminPlain, services.FederationInput{
		OrgID:     ctx.GetOrgID(),
		UserID:    userID,
		UserEmail: req.UserEmail,
	})
	if resolveErr != nil {
		c.JSON(http.StatusOK, openapi.FederationTestResponse{
			Success:     false,
			ProbeStatus: "skipped",
			Error:       resolveErr.Error(),
		})
		return
	}

	// Phase 2 — agent-side probe. Mirror the real session-open merge
	// (see gateway/transport/client.go resolveFederationForSession): start
	// from the candidate static envs, drop the provider-declared
	// SupersededEnvVars, then overlay the federation output on top. This
	// keeps the wizard's "test" verdict honest — if a real session would
	// run without GOOGLE_APPLICATION_CREDENTIALS, so should the probe.
	probeEnvs := make(map[string]string, len(req.Connection.Envs)+len(res.EnvVars))
	for k, v := range req.Connection.Envs {
		probeEnvs[k] = v
	}
	supersededInTest := make([]string, 0, len(res.SupersededEnvVars))
	for _, name := range res.SupersededEnvVars {
		if _, ok := probeEnvs[name]; ok {
			supersededInTest = append(supersededInTest, name)
		}
		delete(probeEnvs, name)
	}
	for k, v := range res.EnvVars {
		probeEnvs[k] = v
	}

	// SID is synthetic and namespaces this probe in the agent's logs.
	// The agent does not persist anything keyed by SID for BareExec, so
	// uniqueness is the only requirement.
	probeSID := "federation-probe-" + uuid.NewString()
	probeReq := &pbsystem.BareExecRequest{
		SID:     probeSID,
		AgentID: req.Connection.AgentID,
		Script:  req.Connection.TestScript,
		Command: req.Connection.Command,
		EnvVars: probeEnvs,
	}
	probeResp := transportsystem.BareExecWithTimeout(probeReq, federationTestTimeout)

	envKeys := make([]string, 0, len(res.EnvVars))
	for k := range res.EnvVars {
		envKeys = append(envKeys, k)
	}

	probeOK := probeResp.Status == pbsystem.StatusSuccessType
	c.JSON(http.StatusOK, openapi.FederationTestResponse{
		Success:           probeOK,
		ResolvedPrincipal: res.ResolvedPrincipal,
		AdminPrincipal:    res.AdminPrincipal,
		EnvVarKeys:        envKeys,
		SupersededEnvVars: supersededInTest,
		TokenExpiresAt:    res.TokenExpiresAt.Format(time.RFC3339),
		ProbeStatus:       probeResp.Status,
		ProbeOutput:       probeResp.Output,
	})
}

// validateProbeConnection enforces the minimum shape the BareExec dispatch
// needs. The fields are required at the binding layer too (binding:"required"
// on the struct tags); this function catches the empty-slice and
// empty-string cases binding does not catch (Gin's required validator
// rejects nil pointers but accepts empty slices and "" values).
func validateProbeConnection(conn openapi.FederationTestConnection) error {
	if conn.AgentID == "" {
		return errBadRequest("connection.agent_id is required")
	}
	if len(conn.Command) == 0 || conn.Command[0] == "" {
		return errBadRequest("connection.command is required (argv slice with at least the binary name)")
	}
	if conn.TestScript == "" {
		return errBadRequest("connection.test_script is required")
	}
	return nil
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

	// Dry-render the identity template now so typo placeholders (e.g.
	// {user.handle} instead of {user.email_local}) surface as a 400 here
	// rather than as a runtime federation failure on the first session.
	// The synthetic context populates every supported source so we exercise
	// the full substitution surface. Empty-source failures are a runtime
	// concern (per-user), not a config-validity concern.
	if err := dryRenderIdentityTemplate(req); err != nil {
		return err
	}
	return nil
}

// dryRenderIdentityTemplate runs the configured source/template combo against
// a stable synthetic user. It returns a 400-ready error when the template uses
// an unsupported placeholder or renders to empty against a fully populated
// context. Both are config-time mistakes the admin can fix in-place.
//
// We intentionally do not enforce provider-specific shape rules here (e.g. SA
// local-part regex for gcp_iam): admins routinely tweak templates against
// users whose emails would not yet exist. Provider-side validation lives in
// the resolver (gcpiam.preflightServiceAccountPrincipal) and surfaces a clear
// error at session open if the rendered principal is unusable for that
// provider.
func dryRenderIdentityTemplate(req openapi.ConnectionFederationConfig) error {
	const (
		probeEmail = "validation-probe@example.com"
		probeID    = "00000000-0000-0000-0000-000000000000"
	)
	if _, err := federation.ResolveIdentity(req.IdentitySourceAttribute, req.IdentityTargetTemplate, federation.IdentityContext{
		UserEmail: probeEmail,
		UserID:    probeID,
	}); err != nil {
		return errBadRequest("identity template validation failed: %v", err)
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
