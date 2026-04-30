package mcpauth

import (
	"errors"
	"fmt"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	oidcprovider "github.com/hoophq/hoop/gateway/idp/oidc"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type effectiveConfig struct {
	Enabled     bool
	ResourceURI string
	GroupsClaim string
	IssuerURL   string
	OrgID       string
	GrpcURL     string
	Provider    idptypes.ProviderType
}

func loadConfig() (effectiveConfig, bool) {
	serverAuthConfig, providerType, err := idp.LoadServerAuthConfig()
	if err != nil {
		log.Warnf("mcp oauth: failed loading server auth config, reason=%v", err)
		return effectiveConfig{}, false
	}
	if providerType != idptypes.ProviderTypeOIDC && providerType != idptypes.ProviderTypeIDP {
		return effectiveConfig{Provider: providerType}, true
	}
	oidcOpts, err := idp.NewOidcProviderOptions(serverAuthConfig)
	if err != nil || oidcOpts.IssuerURL == "" {
		return effectiveConfig{Provider: providerType}, true
	}
	cfg := effectiveConfig{
		IssuerURL: oidcOpts.IssuerURL,
		Provider:  providerType,
	}
	if serverAuthConfig != nil {
		cfg.OrgID = serverAuthConfig.OrgID
		if serverAuthConfig.GrpcServerURL != nil {
			cfg.GrpcURL = *serverAuthConfig.GrpcServerURL
		}
	}
	if cfg.GrpcURL == "" {
		cfg.GrpcURL = appconfig.Get().GrpcURL()
	}
	if serverAuthConfig != nil && serverAuthConfig.McpAuthConfig != nil {
		mcp := serverAuthConfig.McpAuthConfig
		cfg.Enabled = mcp.Enabled
		cfg.ResourceURI = mcp.ResourceURI
		cfg.GroupsClaim = mcp.GroupsClaim
	}
	if cfg.ResourceURI == "" {
		cfg.ResourceURI = appconfig.Get().ApiURL() + McpResourcePath()
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = defaultGroupsClaim
	}
	return cfg, true
}

// authenticateOAuth21 validates a JWT bearer per the MCP OAuth 2.1 Resource
// Server profile, materializes the Hoop user context (creating the user on
// first sight), and stores it on the gin.Context so downstream handlers see
// the same shape as a legacy AuthMiddleware request.
func authenticateOAuth21(c *gin.Context, bearer string, cfg effectiveConfig) error {
	verifier, err := idp.NewOidcVerifierProvider()
	if err != nil {
		return fmt.Errorf("oidc verifier unavailable: %w", err)
	}
	provider, ok := verifier.(*oidcprovider.Provider)
	if !ok {
		return fmt.Errorf("oidc verifier does not support resource-bound validation")
	}

	uinfo, claims, err := provider.VerifyAccessTokenForResource(bearer, cfg.ResourceURI)
	if err != nil {
		return err
	}
	if uinfo.Subject == "" {
		return errors.New("token is missing required 'sub' claim")
	}

	groups := extractGroups(claims, cfg.GroupsClaim)
	uinfo.Groups = groups

	ctx, err := syncMcpUser(cfg.OrgID, uinfo)
	if err != nil {
		return fmt.Errorf("user sync failed: %w", err)
	}
	if ctx.UserStatus != string(types.UserStatusActive) {
		return fmt.Errorf("user is not active, status=%s", ctx.UserStatus)
	}

	storeContext(c, ctx, cfg)
	return nil
}

func storeContext(c *gin.Context, ctx *models.Context, cfg effectiveConfig) {
	c.Set(storagev2.ContextKey,
		storagev2.NewContext(ctx.UserSubject, ctx.OrgID).
			WithUserInfo(ctx.UserName, ctx.UserEmail, ctx.UserStatus, ctx.UserPicture, ctx.UserGroups).
			WithSlackID(ctx.UserSlackID).
			WithOrgName(ctx.OrgName).
			WithOrgLicenseData(ctx.OrgLicenseData).
			WithApiURL(appconfig.Get().ApiURL()).
			WithGrpcURL(cfg.GrpcURL).
			WithProviderType(cfg.Provider),
	)
}

// extractGroups normalises the configurable groups claim into a string slice.
// Different IdPs emit groups as either a single string or an array of strings;
// per RFC 7519 both shapes are valid claim values.
func extractGroups(claims map[string]any, claimName string) []string {
	switch v := claims[claimName].(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, raw := range v {
			if s, _ := raw.(string); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return slices.Clone(v)
	}
	return nil
}

// syncMcpUser materializes a Hoop user record for the given OAuth 2.1 subject.
// It mirrors the single-tenant OIDC login flow (gateway/api/login/oidc) so an
// MCP-only user shows up in audit, review, slack, and access-control plugins
// identical to a web-UI user with the same subject.
func syncMcpUser(orgID string, uinfo idptypes.ProviderUserInfo) (*models.Context, error) {
	ctx, err := models.GetUserContext(uinfo.Subject)
	if err != nil {
		return nil, err
	}
	if !ctx.IsEmpty() {
		if err := refreshExistingUser(ctx, uinfo); err != nil {
			return nil, err
		}
		return models.GetUserContext(uinfo.Subject)
	}

	if appconfig.Get().OrgMultitenant() {
		return nil, errors.New("multi-tenant deployments require web UI signup before MCP OAuth")
	}

	if uinfo.Email == "" {
		return nil, errors.New("token is missing 'email' claim required for first-time MCP signup")
	}

	if existing, _ := models.GetUserByEmail(uinfo.Email); existing != nil {
		updated := *existing
		updated.Subject = uinfo.Subject
		if err := models.UpdateUser(&updated); err != nil {
			return nil, fmt.Errorf("failed reattaching subject to user: %w", err)
		}
		return models.GetUserContext(uinfo.Subject)
	}

	org, err := models.GetOrganizationByNameOrID(proto.DefaultOrgName)
	if err != nil {
		return nil, fmt.Errorf("failed loading default org: %w", err)
	}
	resolvedOrgID := orgID
	if resolvedOrgID == "" {
		resolvedOrgID = org.ID
	}

	newUser := models.User{
		ID:       uuid.NewString(),
		OrgID:    resolvedOrgID,
		Subject:  uinfo.Subject,
		Name:     uinfo.Profile,
		Email:    uinfo.Email,
		Verified: emailVerified(uinfo),
		Status:   string(types.UserStatusActive),
	}
	if err := models.CreateUser(newUser); err != nil {
		return nil, fmt.Errorf("failed creating mcp user: %w", err)
	}
	if len(uinfo.Groups) > 0 {
		groupRows := make([]models.UserGroup, 0, len(uinfo.Groups))
		for _, g := range uinfo.Groups {
			groupRows = append(groupRows, models.UserGroup{
				OrgID:  resolvedOrgID,
				UserID: newUser.ID,
				Name:   g,
			})
		}
		if err := models.InsertUserGroups(groupRows); err != nil {
			return nil, fmt.Errorf("failed creating mcp user groups: %w", err)
		}
	}
	return models.GetUserContext(uinfo.Subject)
}

func refreshExistingUser(ctx *models.Context, uinfo idptypes.ProviderUserInfo) error {
	updatedUser := models.User{
		ID:       ctx.UserID,
		OrgID:    ctx.OrgID,
		Subject:  uinfo.Subject,
		Name:     coalesce(uinfo.Profile, ctx.UserName),
		Email:    coalesce(uinfo.Email, ctx.UserEmail),
		Verified: emailVerified(uinfo),
		Status:   string(types.UserStatusActive),
		SlackID:  ctx.UserSlackID,
	}
	groupRows := make([]models.UserGroup, 0, len(uinfo.Groups))
	for _, g := range mergeAdmin(uinfo.Groups, ctx) {
		groupRows = append(groupRows, models.UserGroup{
			OrgID:  ctx.OrgID,
			UserID: ctx.UserID,
			Name:   g,
		})
	}
	if err := models.UpdateUserAndUserGroups(&updatedUser, groupRows); err != nil {
		return fmt.Errorf("failed updating mcp user: %w", err)
	}
	return nil
}

// mergeAdmin preserves a pre-existing admin grant so an MCP login from an IdP
// that does not emit the admin group cannot accidentally demote a Hoop admin.
func mergeAdmin(groups []string, ctx *models.Context) []string {
	deduped := dedupe(groups)
	if ctx.IsAdmin() && !slices.Contains(deduped, types.GroupAdmin) {
		deduped = append(deduped, types.GroupAdmin)
	}
	return deduped
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func emailVerified(uinfo idptypes.ProviderUserInfo) bool {
	if uinfo.EmailVerified == nil {
		return false
	}
	return *uinfo.EmailVerified
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
