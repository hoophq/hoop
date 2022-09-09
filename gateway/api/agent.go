package api

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/domain"
	"net/http"
)

func (a *Api) PostAgent(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*domain.Context)

	var agent domain.Agent
	if err := c.ShouldBindJSON(&agent); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	agent.Token = "x-agt-" + uuid.NewString()
	agent.OrgId = context.Org.Id

	_, err := a.storage.PersistAgent(&agent)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, agent)
}

func (a *Api) GetAgents(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*domain.Context)

	connections, err := a.storage.GetAgents(context)
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, connections)
}
