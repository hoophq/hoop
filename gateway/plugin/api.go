package plugin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		Persist(context *user.Context, plugin *Plugin) error
		FindAll(context *user.Context) ([]ListPlugin, error)
		FindOne(context *user.Context, name string) (*Plugin, error)
	}
)

func (a *Handler) FindOne(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	name := c.Param("name")
	plugin, err := a.Service.FindOne(context, name)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if plugin == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	c.PureJSON(http.StatusOK, plugin)
}

func (a *Handler) FindAll(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	plugins, err := a.Service.FindAll(context)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.PureJSON(http.StatusOK, plugins)
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

	if err = a.Service.Persist(context, &plugin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, plugin)
}

func (a *Handler) Put(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	name := c.Param("name")
	existingPlugin, err := a.Service.FindOne(context, name)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if existingPlugin == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	var plugin Plugin
	if err := c.ShouldBindJSON(&plugin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if plugin.Id == "" {
		plugin.Id = existingPlugin.Id
	}

	if err = a.Service.Persist(context, &plugin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.PureJSON(http.StatusOK, plugin)
}
