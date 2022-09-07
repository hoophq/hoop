package api

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/domain"
	"net/http"
)

func (a *Api) GetConnection(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*domain.Context)

	name := c.Param("name")
	connections, err := a.storage.GetConnection(context, name)
	if err != nil {
		c.Error(err)
		return
	}

	c.PureJSON(http.StatusOK, connections)
}

func (a *Api) GetConnections(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*domain.Context)

	connections, err := a.storage.GetConnections(context)
	if err != nil {
		c.Error(err)
		return
	}

	c.PureJSON(http.StatusOK, connections)
}

func (a *Api) PostConnection(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*domain.Context)

	var connection domain.ConnectionOne
	if err := c.ShouldBindJSON(&connection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	existingCon, err := a.storage.GetConnection(context, connection.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if existingCon != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Connection already exists."})
		return
	}

	tx, err := a.storage.PersistConnection(context, &connection)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	fmt.Printf("tx: %d", tx)

	c.JSON(http.StatusCreated, connection)
}
