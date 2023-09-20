package agent

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/dsnkeys"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2"
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
		FindByToken(token string) (*Agent, error)
		Evict(xtID string) error
	}
)

type AgentRequest struct {
	Name string `json:"name" binding:"required"`
	Mode string `json:"mode"`
}

var rfc1035Err = "invalid name. It must contain 63 characters; start and end with alphanumeric lowercase character or contains '-'"

func isValidRFC1035LabelName(label string) bool {
	re := regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)
	if len(label) > 63 || !re.MatchString(label) {
		return false
	}
	return true
}

func (s *Handler) Post(c *gin.Context) {
	ctx := user.ContextUser(c)
	ctxv2 := storagev2.ParseContext(c)
	log := user.ContextLogger(c)

	req := AgentRequest{Mode: pb.AgentModeStandardType}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Infof("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if !isValidRFC1035LabelName(req.Name) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": rfc1035Err})
		return
	}

	existentAgent, err := s.Service.FindByNameOrID(ctx, req.Name)
	if err != nil {
		log.Errorf("failed validating agent, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if existentAgent != nil {
		log.Errorf("agent %v already exists", req.Name)
		c.JSON(http.StatusConflict, gin.H{"message": "agent already exists"})
		return
	}

	secretKey, secretKeyHash, err := dsnkeys.GenerateSecureRandomKey()
	if err != nil {
		log.Errorf("failed generating agent token, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating agent token"})
		return
	}

	var dsn string
	switch req.Mode {
	case pb.AgentModeEmbeddedType:
		// this mode negotiates the grpc url with the api.
		// In the future we may consolidate to use the grpc url instead
		dsn, err = dsnkeys.NewString(ctxv2.ApiURL, req.Name, secretKey, pb.AgentModeEmbeddedType)
	case pb.AgentModeStandardType:
		dsn, err = dsnkeys.NewString(ctxv2.GrpcURL, req.Name, secretKey, pb.AgentModeStandardType)
	default:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("unknown agent mode %q", req.Mode)})
		return
	}
	if err != nil {
		log.Errorf("failed generating dsn, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating dsn"})
		return
	}

	agt := Agent{
		// a deterministic uuid allows automatic reasign of resources
		// in case of removal and creating with the same name (e.g. connections)
		Id:    deterministicAgentUUID(ctx.Org.Id, req.Name),
		Name:  req.Name,
		OrgId: ctx.Org.Id,
		Token: secretKeyHash,
		Mode:  req.Mode,
	}
	if _, err := s.Service.Persist(&agt); err != nil {
		log.Errorf("failed persisting agent token, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, map[string]string{"token": dsn})
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
