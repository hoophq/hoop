package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

type (
	Connection struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Secret string `json:"secret"`
	}
)

func (a *Api) GetConnections(c *gin.Context) {
	connections, err := a.repository.GetConnections()
	if err != nil {
		c.Error(err)
	}
	c.JSON(http.StatusOK, connections)
}

func (a *Api) PostConnection(c *gin.Context) {
	var connection Connection
	if err := c.ShouldBindJSON(&connection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if err := a.repository.PersistConnection(connection); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}

	c.JSON(http.StatusCreated, connection)
}
