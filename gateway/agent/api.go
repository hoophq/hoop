package agent

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		Persist(agent *Agent) (int64, error)
		FindAll(context *user.Context) ([]Agent, error)
	}
)

func (s *Handler) Post(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	var a Agent
	if err := c.ShouldBindJSON(&a); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	a.Id = uuid.NewString()
	a.Token = "x-agt-" + uuid.NewString()
	a.OrgId = context.Org.Id

	_, err := s.Service.Persist(&a)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, a)
}

func (s *Handler) FindAll(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	connections, err := s.Service.FindAll(context)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, connections)
}
