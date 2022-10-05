package plugin

import (
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/user"
	"net/http"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		Persist(context *user.Context, c *Plugin) (int64, error)
		FindAll(context *user.Context) ([]Plugin, error)
		FindOne(context *user.Context, name string) (*Plugin, error)
	}
)

func (a *Handler) FindOne(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	name := c.Param("name")
	connection, err := a.Service.FindOne(context, name)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if connection == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	c.PureJSON(http.StatusOK, connection)
}

func (a *Handler) FindAll(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	connections, err := a.Service.FindAll(context)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.PureJSON(http.StatusOK, connections)
}

func (a *Handler) Post(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	var plugin Plugin
	if err := c.ShouldBindJSON(&plugin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingPlugin, err := a.Service.FindOne(context, plugin.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingPlugin != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Plugin already installed."})
		return
	}

	_, err = a.Service.Persist(context, &plugin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, plugin)
}
