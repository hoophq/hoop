package api

import (
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/domain"
	"net/http"
)

func (a *Api) GetConnections(c *gin.Context) {
	connections, err := a.storage.getConnections()
	if err != nil {
		c.Error(err)
	}
	c.JSON(http.StatusOK, connections)
}

func (a *Api) PostConnection(c *gin.Context) {
	var connection domain.Connection
	if err := c.ShouldBindJSON(&connection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := a.storage.persistConnection(connection); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}

	c.JSON(http.StatusCreated, connection)
}
