package agent

import (
	"fmt"
	"net/http"

	"github.com/getsentry/sentry-go"
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
		FindByNameOrID(ctx *user.Context, name string) (*Agent, error)
		Evict(xtID string) error
	}
)

func (s *Handler) Post(c *gin.Context) {
	ctx := user.ContextUser(c)
	user.ContextLogger(c).Warnf("POST /api/agents is deprecated, user must use client keys instead")

	sentry.CaptureException(fmt.Errorf("POST /api/agents is deprecated, user=%v, org=%v",
		ctx.User.Email, ctx.Org.Name))
	c.JSON(http.StatusGone, gin.H{"message": "endpoint deprecated, use clientkeys instead"})
}

func (s *Handler) Evict(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	nameOrID := c.Param("nameOrID")
	agent, err := s.Service.FindByNameOrID(ctx, nameOrID)
	if err != nil {
		log.Errorf("failed fetching agent, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if agent == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	if agent.Id == "" {
		log.Errorf("agent with empty xtid, agent=%v", agent)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if err := s.Service.Evict(agent.Id); err != nil {
		log.Errorf("failed evicting agent %v, err=%v", agent.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.Writer.WriteHeader(204)
}

func (s *Handler) FindAll(c *gin.Context) {
	context := user.ContextUser(c)

	connections, err := s.Service.FindAll(context)
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, connections)
}

func validateToken(token string) error {
	// x-agt-[UUID]
	if len(token) < 7 {
		return fmt.Errorf("invalid token length")
	}
	_, err := uuid.Parse(token[6:])
	if err != nil {
		return fmt.Errorf("invalid token, err=%v", err)
	}
	return nil
}
