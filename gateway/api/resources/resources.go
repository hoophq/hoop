package resources

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
	transportsystem "github.com/hoophq/hoop/gateway/transport/system"
	"gorm.io/gorm"
)

// GetResource
//
//	@Summary		Gets a resource
//	@Description	Gets a resource by ID for the organization.
//	@Tags			Resources
//	@Produces		json
//	@Param			name		path		string	true	"The resource name"
//	@Success		200			{object}	openapi.ResourceResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/resources/{name} [get]
func GetResource(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	name := c.Param("name")

	resource, err := models.GetResourceByName(models.DB, ctx.OrgID, name, ctx.IsAdmin() || ctx.IsAuditor())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource: %v", err)
		return
	}
	if resource == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	c.JSON(http.StatusOK, toOpenApi(resource))
}

// CreateResource
//
//	@Summary		Creates a resource
//	@Description	Creates a resource for the organization.
//	@Tags			Resources
//	@Produces		json
//	@Param			request		body		openapi.ResourceRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.ResourceResponse
//	@Failure		400,403,500	{object}	openapi.HTTPError
//	@Router			/resources [post]
func CreateResource(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.ResourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.SubType == "" {
		req.SubType = req.Type
	}

	existing, err := models.GetResourceByName(models.DB, ctx.OrgID, req.Name, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource: %v", err)
		return
	}
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "resource name already exists"})
		return
	}

	resource := models.Resources{
		OrgID:   ctx.OrgID,
		Name:    req.Name,
		Type:    req.Type,
		SubType: sql.NullString{String: req.SubType, Valid: req.SubType != ""},
		Envs:    req.EnvVars,
		AgentID: sql.NullString{String: req.AgentID, Valid: req.AgentID != ""},
	}

	var connections []*models.Connection
	if len(req.Roles) > 0 {
		adminCtx := models.NewAdminContext(ctx.OrgID)
		for _, role := range req.Roles {
			foundConnection, _ := models.GetBareConnectionByNameOrID(adminCtx, role.Name, models.DB)
			if foundConnection != nil {
				c.JSON(http.StatusConflict, gin.H{"message": "connection with the same name, " + role.Name})
				return
			}

			defaultCmd, defaultEnvVars := apiconnections.GetConnectionDefaults(role.Type, role.SubType, true)

			if len(role.Command) == 0 {
				role.Command = defaultCmd
			}

			for key, val := range defaultEnvVars {
				if _, isset := role.Secrets[key]; !isset {
					role.Secrets[key] = val
				}
			}

			accessSchemaStatus := "disabled"
			if role.Type == "database" {
				accessSchemaStatus = "enabled"
			}

			agentId := resource.AgentID.String
			if role.AgentID != "" {
				agentId = role.AgentID
			}

			connectionStatus := models.ConnectionStatusOffline
			if streamclient.IsAgentOnline(streamtypes.NewStreamID(agentId, "")) {
				connectionStatus = models.ConnectionStatusOnline
			}

			connections = append(connections, &models.Connection{
				OrgID:              ctx.OrgID,
				Name:               role.Name,
				ResourceName:       req.Name,
				AgentID:            sql.NullString{String: role.AgentID, Valid: role.AgentID != ""},
				Type:               role.Type,
				SubType:            sql.NullString{String: role.SubType, Valid: role.SubType != ""},
				Command:            role.Command,
				Status:             connectionStatus,
				AccessModeRunbooks: "enabled",
				AccessModeExec:     "enabled",
				AccessModeConnect:  "enabled",
				AccessSchema:       accessSchemaStatus,
				Envs:               apiconnections.CoerceToMapString(role.Secrets),
				ConnectionTags:     map[string]string{},
			})
		}
	}

	sess := &gorm.Session{FullSaveAssociations: true}
	err = models.DB.Session(sess).Transaction(func(tx *gorm.DB) error {
		// Insert resource
		err = models.UpsertResource(tx, &resource, true)
		if err != nil {
			log.Errorf("failed to upsert resource: %v", err)
			return err
		}

		// Insert connections
		if len(connections) > 0 {
			err = models.UpsertBatchConnections(tx, connections)
			if err != nil {
				log.Errorf("failed to upsert batch connections: %v", err)
				return err
			}
		}

		return nil
	})

	evt := audit.NewEvent(audit.ResourceResource, audit.ActionCreate).
		Resource(resource.ID, resource.Name).
		Set("name", req.Name).
		Set("type", req.Type).
		Set("subtype", req.SubType).
		Set("agent_id", req.AgentID)
	defer func() { evt.Log(c) }()

	if err != nil {
		evt.Err(err)
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating resource: %v", err)
		return
	}

	c.JSON(http.StatusCreated, toOpenApi(&resource))
}

func validateListOptions(urlValues url.Values) (o models.ResourceFilterOption, err error) {
	pageStr := urlValues.Get("page")
	pageSizeStr := urlValues.Get("page_size")
	page, pageSize, paginationErr := apivalidation.ParsePaginationParams(pageStr, pageSizeStr)
	if paginationErr != nil {
		return o, paginationErr
	}

	o.Page = page
	o.PageSize = pageSize

	for key, values := range urlValues {
		switch key {
		case "search":
			o.Search = values[0]
		case "name":
			o.Name = values[0]
		case "subtype":
			o.SubType = values[0]
		}
	}
	return
}

// ListResources
//
//	@Summary		Lists resources
//	@Description	Lists all resources for the organization.
//	@Tags			Resources
//	@Produces		json
//	@Param			search	query		string	false	"Search by name, type, subtype"	Format(string)
//	@Param			name	query		string	false	"Filter by name"				Format(string)
//	@Param			subtype	query		string	false	"Filter by subtype"				Format(string)
//	@Success		200		{object}	openapi.PaginatedResponse[openapi.ResourceResponse]
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/resources [get]
func ListResources(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	queryParams := c.Request.URL.Query()

	opts, err := validateListOptions(queryParams)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	resources, total, err := models.ListResources(models.DB, ctx.OrgID, ctx.UserGroups, ctx.IsAdmin() || ctx.IsAuditor(), opts)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing resources: %v", err)
		return
	}

	var resp []*openapi.ResourceResponse
	for _, r := range resources {
		resp = append(resp, toOpenApi(&r))
	}

	// Backwards compatibility: return a bare array when no pagination params are
	// present, matching the pre-pagination response format used by older clients.
	if queryParams.Get("page") == "" && queryParams.Get("page_size") == "" {
		if resp == nil {
			resp = []*openapi.ResourceResponse{}
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	response := openapi.PaginatedResponse[*openapi.ResourceResponse]{
		Pages: openapi.Pagination{
			Total: int(total),
			Page:  opts.Page,
			Size:  opts.PageSize,
		},
		Data: resp,
	}

	c.JSON(http.StatusOK, response)
}

// UpdateResource
//
//	@Summary		Updates a resource
//	@Description	Updates a resource by ID for the organization.
//	@Tags			Resources
//	@Produces		json
//	@Param			name		path		string					true	"The resource name"
//	@Param			request		body		openapi.ResourceRequest	true	"The request body resource"
//	@Success		200			{object}	openapi.ResourceResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/resources/{name} [put]
func UpdateResource(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	name := c.Param("name")

	var req openapi.ResourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.SubType == "" {
		req.SubType = req.Type
	}

	existing, err := models.GetResourceByName(models.DB, ctx.OrgID, name, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource: %v", err)
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	connections, err := models.GetResourceConnections(models.DB, ctx.OrgID, name)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource connections: %v", err)
		return
	}
	if len(connections) > 0 && (existing.Type != req.Type || existing.SubType.String != req.SubType) {
		c.JSON(http.StatusForbidden, gin.H{"message": "cannot change resource type or subtype with existing connections"})
		return
	}

	resource := models.Resources{
		ID:      existing.ID,
		OrgID:   ctx.OrgID,
		Name:    req.Name,
		Type:    req.Type,
		SubType: sql.NullString{String: req.SubType, Valid: req.SubType != ""},
		Envs:    req.EnvVars,
		AgentID: sql.NullString{String: req.AgentID, Valid: req.AgentID != ""},
	}

	evt := audit.NewEvent(audit.ResourceResource, audit.ActionUpdate).
		Resource(resource.ID, resource.Name).
		Set("name", req.Name).
		Set("type", req.Type).
		Set("subtype", req.SubType)
	defer func() { evt.Log(c) }()

	err = models.UpsertResource(models.DB, &resource, true)
	if err != nil {
		evt.Err(err)
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating resource: %v", err)
		return
	}

	c.JSON(http.StatusOK, toOpenApi(&resource))
}

// DeleteResource
//
//	@Summary		Deletes a resource
//	@Description	Deletes a resource by ID for the organization.
//	@Tags			Resources
//	@Produces		json
//	@Param			name	path	string	true	"The resource name"
//	@Success		204
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/resources/{name} [delete]
func DeleteResource(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	name := c.Param("name")

	resource, err := models.GetResourceByName(models.DB, ctx.OrgID, name, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource: %v", err)
		return
	}
	if resource == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	connections, err := models.GetResourceConnections(models.DB, ctx.OrgID, name)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource connections: %v", err)
		return
	}
	if len(connections) > 0 {
		c.JSON(http.StatusForbidden, gin.H{"message": "cannot delete resource with existing connections"})
		return
	}

	evt := audit.NewEvent(audit.ResourceResource, audit.ActionDelete).
		Resource(name, name)
	defer func() { evt.Log(c) }()

	err = models.DeleteResource(models.DB, ctx.OrgID, name)
	if err != nil {
		evt.Err(err)
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting resource: %v", err)
		return
	}

	c.Status(http.StatusNoContent)
}

// CreateResourceRole
//
//	@Summary		Creates external roles for a resource
//	@Description	Grants an external role on a target system using the resource's master credentials. The operation runs asynchronously; use the sessions endpoint to track progress.
//	@Tags			Resources
//	@Accept			json
//	@Produces		json
//	@Param			name		path		string								true	"The resource name"
//	@Param			request		body		openapi.ResourceRoleGrantRequest	true	"The request body"
//	@Success		202			{object}	openapi.ResourceRoleGrantResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/resources/{name}/roles [post]
func CreateResourceRole(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	name := c.Param("name")

	var req openapi.ResourceRoleGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	resource, err := models.GetResourceByName(models.DB, ctx.OrgID, name, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed retrieving resource: %v", err)
		return
	}
	if resource == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	jobResp, err := transportsystem.RunResourceManager(
		// TODO: accept agent id from the request body
		resource.AgentID.String,
		&pbsystem.ResourceManagerRequest{
			OrgID:          ctx.OrgID,
			UserID:         ctx.UserID,
			UserName:       ctx.UserName,
			UserEmail:      ctx.UserEmail,
			ResourceName:   name,
			ResourceType:   resource.Type,
			ConnectionName: req.Name,
			RoleName:       req.RoleName,
			Script:         resourceManagerScript(resource.SubType.String),
			TemplateData:   map[string]any{"role_name": req.RoleName, "password": ""},
			Command:        resourceManagerCommand(resource.SubType.String),
			EnvVars:        resourceManagerEnvVars(resource.Envs),
		},
	)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusBadRequest, err, "failed starting resource manager: %v", err)
		return
	}

	c.JSON(http.StatusAccepted, openapi.ResourceRoleGrantResponse{
		SessionID: jobResp.SessionID,
		Tags:      jobResp.Tags,
		Status:    jobResp.Status,
	})
}

// resourceManagerCommand returns the runtime entrypoint for the given database subtype.
func resourceManagerCommand(subType string) []string {
	switch subType {
	case "postgres":
		return []string{"psql"}
	}
	return nil
}

// resourceManagerScript returns a Go template script for the given database subtype.
// The template exposes {{.role_name}} and {{.password}} placeholders.
// For now each variant is a bare SELECT used to verify connectivity.
func resourceManagerScript(subType string) string {
	switch subType {
	case "postgres":
		return `SELECT '{{.password}}' AS role_password, '{{.role_name}}' AS role_name;`
	}
	return ""
}

// resourceManagerEnvVars extracts the subset of resource env vars required by the
// subtype's runtime entrypoint, stripping the "envvar:" key prefix.
// For postgres this yields PGHOST, PGPORT, PGUSER, PGPASSWORD, PGDATABASE.
func resourceManagerEnvVars(envs map[string]string) map[string]string {
	result := make(map[string]string, len(envs))
	for k, v := range envs {
		if key, ok := strings.CutPrefix(k, "envvar:"); ok {
			result[key] = v
		}
	}
	return result
}

func toOpenApi(r *models.Resources) *openapi.ResourceResponse {
	return &openapi.ResourceResponse{
		ID:        r.ID,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
		Name:      r.Name,
		Type:      r.Type,
		SubType:   r.SubType.String,
		EnvVars:   r.Envs,
		AgentID:   r.AgentID.String,
	}
}
