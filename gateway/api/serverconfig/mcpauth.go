package apiserverconfig

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// GetMcpAuthConfig
//
//	@Summary		Get MCP OAuth 2.1 Resource Server Configuration
//	@Description	Returns the per-org MCP OAuth Resource Server settings. When disabled (default), /mcp accepts Hoop-issued bearer tokens only.
//	@Tags			Server Management
//	@Produce		json
//	@Success		200		{object}	openapi.ServerMcpAuthConfig
//	@Failure		403,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/mcp-auth [get]
func GetMcpAuthConfig(c *gin.Context) {
	if forbidden := forbiddenOnMultiTenantSetups(c); forbidden {
		return
	}
	cfg, err := models.GetServerAuthConfig()
	if err != nil && err != models.ErrNotFound {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to get server auth config")
		return
	}
	c.JSON(http.StatusOK, toMcpAuthOpenApi(cfg))
}

// UpdateMcpAuthConfig
//
//	@Summary		Update MCP OAuth 2.1 Resource Server Configuration
//	@Description	Enable, disable, or reconfigure the OAuth 2.1 Resource Server profile for the /mcp endpoint.
//	@Tags			Server Management
//	@Param			request	body	openapi.ServerMcpAuthConfig	true	"The request body resource"
//	@Accept			json
//	@Produce		json
//	@Success		200				{object}	openapi.ServerMcpAuthConfig
//	@Failure		400,403,422,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/mcp-auth [put]
func UpdateMcpAuthConfig(c *gin.Context) {
	if forbidden := forbiddenOnMultiTenantSetups(c); forbidden {
		return
	}
	ctx := storagev2.ParseContext(c)

	var req openapi.ServerMcpAuthConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	existent, err := models.GetServerAuthConfig()
	if err != nil && err != models.ErrNotFound {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to get server auth config")
		return
	}
	if existent == nil {
		existent = &models.ServerAuthConfig{}
	}
	existent.OrgID = ctx.OrgID
	existent.McpAuthConfig = &models.ServerMcpAuthConfig{
		Enabled:     req.Enabled,
		ResourceURI: req.ResourceURI,
		GroupsClaim: req.GroupsClaim,
	}

	evt := audit.NewEvent(audit.ResourceAuthConfig, audit.ActionUpdate).
		Set("scope", "mcp_auth").
		Set("enabled", req.Enabled).
		Set("resource_uri", req.ResourceURI).
		Set("groups_claim", req.GroupsClaim)
	defer func() { evt.Log(c) }()

	resp, err := models.UpdateServerAuthConfig(existent)
	if err != nil {
		evt.Err(err)
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to update mcp auth config")
		return
	}
	c.JSON(http.StatusOK, toMcpAuthOpenApi(resp))
}

func toMcpAuthOpenApi(cfg *models.ServerAuthConfig) *openapi.ServerMcpAuthConfig {
	if cfg == nil || cfg.McpAuthConfig == nil {
		return &openapi.ServerMcpAuthConfig{}
	}
	return &openapi.ServerMcpAuthConfig{
		Enabled:     cfg.McpAuthConfig.Enabled,
		ResourceURI: cfg.McpAuthConfig.ResourceURI,
		GroupsClaim: cfg.McpAuthConfig.GroupsClaim,
	}
}
