package jit

import (
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"
	pb "github.com/runopsio/hoop/common/proto"

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
		Review(context *user.Context, existingJit *Jit, status Status) error
	}
)

func (h *Handler) Put(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	jitId := c.Param("id")
	existingJit, err := h.Service.FindOne(context, jitId)
	if err != nil {
		log.Errorf("failed fetching jit %v, err=%v", jitId, err)
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if existingJit == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	if existingJit.Status != StatusPending {
		c.JSON(http.StatusBadRequest, gin.H{"message": "jit must be at PENDING status"})
		return
	}

	if !pb.IsInList(user.GroupAdmin, context.User.Groups) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "not eligible for jit"})
		return
	}

	var r Jit
	if err := c.ShouldBindJSON(&r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	status := strings.ToUpper(string(r.Status))
	r.Status = Status(status)

	if !(r.Status == StatusApproved || r.Status == StatusRejected) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid status"})
		return
	}

	if err := h.Service.Review(context, existingJit, r.Status); err != nil {
		log.Errorf("failed processing jit, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, existingJit)
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
