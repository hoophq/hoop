package resources

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// GetResource
//
//	@Summary		Gets a resource
//	@Description	Gets a resource by ID for the organization.
//	@Tags			Resources
//	@Produces		json
//	@Param			name	path		string	true	"The resource name"
//	@Success		200	{object}	openapi.ResourceResponse
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
//	@Param			request	body	openapi.ResourceRequest	true	"The request body resource"
//	@Success		201	{object}	openapi.ResourceResponse
//	@Failure		400,500	{object}	openapi.HTTPError
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
		c.JSON(http.StatusForbidden, gin.H{"message": "resource name already exists"})
		return
	}

	resource := models.Resources{
		OrgID: ctx.OrgID,
		Name:  req.Name,
		Type:  req.Type,
		Envs:  req.EnvVars,
	}

	err = models.UpsertResource(models.DB, &resource, true)

	if err != nil {
		log.Errorf("failed to upsert resource: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}

	c.JSON(http.StatusCreated, toOpenApi(&resource))
}

// ListResources
//
//	@Summary		Lists resources
//	@Description	Lists all resources for the organization.
//	@Tags			Resources
//	@Produces		json
//	@Success		200	{array}	openapi.ResourceResponse
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/resources [get]
func ListResources(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	resources, err := models.ListResources(models.DB, ctx.OrgID, ctx.IsAdmin())
	if err != nil {
		log.Errorf("failed to list resources: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}

	var resp []openapi.ResourceResponse
	for _, r := range resources {
		resp = append(resp, *toOpenApi(&r))
	}

	c.JSON(http.StatusOK, resp)
}

// UpdateResource
//
//	@Summary		Updates a resource
//	@Description	Updates a resource by ID for the organization.
//	@Tags			Resources
//	@Produces		json
//	@Param			name	path		string					true	"The resource name"
//	@Param			request	body		openapi.ResourceRequest	true	"The request body resource"
//	@Success		200	{object}	openapi.ResourceResponse
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
	if len(connections) > 0 && resource.Type != req.Type {
		c.JSON(http.StatusForbidden, gin.H{"message": "cannot change resource type with existing connections"})
		return
	}

	resource.Name = req.Name
	resource.Type = req.Type
	resource.Envs = req.EnvVars

	err = models.UpsertResource(models.DB, resource, true)
	if err != nil {
		log.Errorf("failed to upsert resource: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, toOpenApi(resource))
}

// DeleteResource
//
//	@Summary		Deletes a resource
//	@Description	Deletes a resource by ID for the organization.
//	@Tags			Resources
//	@Produces		json
//	@Param			name	path		string	true	"The resource name"
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
		EnvVars:   r.Envs,
	}
}
