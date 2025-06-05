package apipluginconnections

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

func CreatePluginConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.PluginConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	resourceID := uuid.NewString()
	resource := &models.PluginConnection{
		ID:           resourceID,
		OrgID:        ctx.OrgID,
		PluginID:     req.PluginID,
		ConnectionID: req.ConnectionID,
		Enabled:      true,
		Config:       req.Config,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	err := models.CreatePluginConnection(resource)
	if err != nil {
		log.Errorf("failed creating plugin connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, toOpenApi(resource))
}

func UpdatePluginConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.PluginConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	resource := &models.PluginConnection{
		ID:           c.Param("id"),
		OrgID:        ctx.OrgID,
		PluginID:     req.PluginID,
		ConnectionID: req.ConnectionID,
		Enabled:      true,
		Config:       req.Config,
		UpdatedAt:    time.Now().UTC(),
	}
	err := models.UpdatePluginConnection(resource)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusOK, toOpenApi(resource))
	default:
		log.Errorf("failed updating plugin connection, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

func GetPluginConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	resource, err := models.GetPluginConnection(ctx.OrgID, c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
		return
	case nil:
		c.JSON(http.StatusOK, toOpenApi(resource))
	default:
		log.Errorf("failed fetching plugin connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
}

func DeletePluginConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	err := models.DeletePluginConnection(ctx.OrgID, c.Param("id"))
	if err != nil {
		log.Errorf("failed removing plugin connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func toOpenApi(obj *models.PluginConnection) openapi.PluginConnection {
	return openapi.PluginConnection{
		ID:           obj.ID,
		PluginID:     obj.PluginID,
		ConnectionID: obj.ConnectionID,
		Config:       obj.Config,
		UpdatedAt:    obj.UpdatedAt,
	}
}
