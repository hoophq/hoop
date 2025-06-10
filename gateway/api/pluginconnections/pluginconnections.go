package apipluginconnections

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// UpdatePluginConnection
//
//	@Summary		Upsert Plugin Connection
//	@Description	Update or create a plugin connection resource
//	@Tags			Plugins
//	@Accept			json
//	@Produce		json
//	@Param			name		path		string							true	"The name of the plugin"
//	@Param			id			path		string							true	"The connection id"
//	@Param			request		body		openapi.PluginConnectionRequest	true	"The request body resource"
//	@Success		200			{object}	openapi.PluginConnection
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/plugins/{name}/connections/{id} [put]
func UpsertPluginConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	req := openapi.PluginConnectionRequest{Config: []string{}}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	pluginName := c.Param("name")
	if pluginName == plugintypes.PluginReviewName || pluginName == plugintypes.PluginDLPName {
		c.JSON(http.StatusBadRequest, gin.H{"message": "unable to manage review or dlp plugins, use the connection endpoint instead"})
		return
	}
	pluginConn, err := models.UpsertPluginConnection(ctx.OrgID, pluginName, c.Param("id"), req.Config)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "either the plugin or the connection does not exist"})
	case nil:
		c.JSON(http.StatusOK, toOpenApi(pluginConn))
	default:
		log.Errorf("failed updating plugin connection, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// GetPluginConnection
//
//	@Summary		Get Plugin Connection
//	@Description	Get a plugin connection resource
//	@Tags			Plugins
//	@Produce		json
//	@Param			name	path		string	true	"The name of the plugin"
//	@Param			id		path		string	true	"The connection id"
//	@Success		200		{object}	openapi.PluginConnection
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/plugins/{name}/connections/{id} [get]
func GetPluginConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	resource, err := models.GetPluginConnection(ctx.OrgID, c.Param("name"), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "either the plugin or the connection does not exist"})
		return
	case nil:
		c.JSON(http.StatusOK, toOpenApi(resource))
	default:
		log.Errorf("failed fetching plugin connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
}

// DeletePluginConnection
//
//	@Summary		Delete Plugin Connection
//	@Description	Delete a plugin connection resource.
//	@Tags			Plugins
//	@Produce		json
//	@Param			name	path	string	true	"The name of the plugin"
//	@Param			id		path	string	true	"The connection id"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/plugins/{name}/connections/{id} [delete]
func DeletePluginConnection(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	err := models.DeletePluginConnection(ctx.OrgID, c.Param("name"), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "either the plugin or the connection does not exist"})
	case nil:
		c.JSON(http.StatusNoContent, nil)
	default:
		log.Errorf("failed removing plugin connection, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
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
