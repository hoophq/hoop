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
		Update(context *user.Context, c *Connection) (int64, error)
		FindAll(context *user.Context) ([]BaseConnection, error)
		FindOne(context *user.Context, name string) (*Connection, error)
		FindOneById(context *user.Context, id string) (*Connection, error)
	}
)

func (a *Handler) FindOne(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	name := c.Param("name")
	connection, err := a.Service.FindOne(context, name)
	if err != nil {
		c.Error(err)
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
		c.Error(err)
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
		c.JSON(http.StatusBadRequest, gin.H{"message": "Connection already exists."})
		return
	}

	_, err = a.Service.Persist(context, &connection)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, connection)
}

func (a *Handler) Update(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)
	id := c.Param("id")

	var connection Connection
	connection.Id = id

	if err := c.ShouldBindJSON(&connection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingConById, err := a.Service.FindOneById(context, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingConById == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Connection doesn't exist."})
		return
	}

	existingConByName, err := a.Service.FindOne(context, connection.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingConByName.Id != id && existingConByName.Name == connection.Name {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Connection name is being used."})
		return
	}

	_, err = a.Service.Update(context, &connection)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, connection)
}
