package resources

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
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

	resource, err := models.GetResourceByName(models.DB, ctx.OrgID, name, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Errorf("failed to get resource by name: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
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

	existing, err := models.GetResourceByName(models.DB, ctx.OrgID, req.Name, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Errorf("failed to get resource by name: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
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

	audit.LogFromContextErr(c, audit.ResourceResource, audit.ActionCreate, resource.ID, resource.Name, payloadResourceCreate(req.Name, req.Type, req.SubType, req.AgentID), err)
	if err != nil {
		log.Errorf("failed to create resource: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error, reason: " + err.Error()})
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

	resources, total, err := models.ListResources(models.DB, ctx.OrgID, ctx.UserGroups, ctx.IsAdmin(), opts)
	if err != nil {
		log.Errorf("failed to list resources: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}

	var resp []*openapi.ResourceResponse
	for _, r := range resources {
		resp = append(resp, toOpenApi(&r))
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

	existing, err := models.GetResourceByName(models.DB, ctx.OrgID, name, ctx.IsAdmin())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Errorf("failed to get resource by name: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	connections, err := models.GetResourceConnections(models.DB, ctx.OrgID, name)
	if err != nil {
		log.Errorf("failed to get resource connections: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
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

	err = models.UpsertResource(models.DB, &resource, true)
	audit.LogFromContextErr(c, audit.ResourceResource, audit.ActionUpdate, resource.ID, resource.Name, payloadResourceUpdate(req.Name, req.Type, req.SubType), err)
	if err != nil {
		log.Errorf("failed to upsert resource: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
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
		log.Errorf("failed to get resource by name: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}
	if resource == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
		return
	}

	connections, err := models.GetResourceConnections(models.DB, ctx.OrgID, name)
	if err != nil {
		log.Errorf("failed to get resource connections: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}
	if len(connections) > 0 {
		c.JSON(http.StatusForbidden, gin.H{"message": "cannot delete resource with existing connections"})
		return
	}

	err = models.DeleteResource(models.DB, ctx.OrgID, name)
	audit.LogFromContextErr(c, audit.ResourceResource, audit.ActionDelete, name, name, nil, err)
	if err != nil {
		log.Errorf("failed to delete resource: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}

	c.Status(http.StatusNoContent)
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

func payloadResourceCreate(name, resourceType, subType, agentID string) audit.PayloadFn {
	return func() map[string]any {
		return map[string]any{"name": name, "type": resourceType, "subtype": subType, "agent_id": agentID}
	}
}

func payloadResourceUpdate(name, resourceType, subType string) audit.PayloadFn {
	return func() map[string]any {
		return map[string]any{"name": name, "type": resourceType, "subtype": subType}
	}
}
