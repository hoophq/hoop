package connection

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
		Persist(context *user.Context, c *Connection) (int64, error)
		FindAll(context *user.Context) ([]BaseConnection, error)
		FindOne(context *user.Context, name string) (*Connection, error)
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

	var connection Connection
	if err := c.ShouldBindJSON(&connection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingCon, err := a.Service.FindOne(context, connection.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingCon != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "Connection already exists."})
		return
	}

	_, err = a.Service.Persist(context, &connection)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, connection)
}

func (a *Handler) Put(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	name := c.Param("name")
	existingConnection, err := a.Service.FindOne(context, name)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if existingConnection == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	var connection Connection
	if err := c.ShouldBindJSON(&connection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	connection.Id = existingConnection.Id

	_, err = a.Service.Persist(context, &connection)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, connection)
}
