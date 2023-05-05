package jit

import (
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		FindAll(context *user.Context) ([]Jit, error)
		FindOne(context *user.Context, id string) (*Jit, error)
		Review(context *user.Context, jitID string, status Status) (*Jit, error)
	}
)

func (h *Handler) Put(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	jitID := c.Param("id")
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	status := Status(strings.ToUpper(string(req["status"])))
	if !(status == StatusApproved || status == StatusRejected) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid status"})
		return
	}

	jreview, err := h.Service.Review(context, jitID, status)
	switch err {
	case ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
	case ErrNotEligible, ErrWrongState:
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusOK, jreview)
	default:
		log.Errorf("failed processing jit review, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

func (h *Handler) FindAll(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	jits, err := h.Service.FindAll(context)
	if err != nil {
		log.Errorf("failed listing jits, err=%v", err)
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, jits)
}

func (a *Handler) FindOne(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	id := c.Param("id")
	jit, err := a.Service.FindOne(context, id)
	if err != nil {
		log.Errorf("failed fetching jit %v, err=%v", id, err)
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if jit == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	c.PureJSON(http.StatusOK, jit)
}
