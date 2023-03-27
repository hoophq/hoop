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
	}
)

func (s *Handler) Post(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	var a Agent
	if err := c.ShouldBindJSON(&a); err != nil {
		log.Infof("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	a.Id = uuid.NewString()
	if a.Token == "" {
		a.Token = "x-agt-" + uuid.NewString()
	}
	if err := validateToken(a.Token); err != nil {
		log.Errorf("failed validating agent token, err=%v", err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	a.OrgId = context.Org.Id
	a.CreatedById = context.User.Id

	_, err := s.Service.Persist(&a)
	if err != nil {
		log.Errorf("failed persisting agent token, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, a)
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
