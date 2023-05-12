package review

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/clientexec"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		FindAll(context *user.Context) ([]Review, error)
		FindOne(context *user.Context, id string) (*Review, error)
		Review(context *user.Context, reviewID string, status Status) (*Review, error)
		Revoke(ctx *user.Context, reviewID string) (*Review, error)
		Persist(context *user.Context, review *Review) error
	}
)

func (h *Handler) Put(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	reviewID := c.Param("id")
	var req map[string]string
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	var review *Review
	status := Status(strings.ToUpper(string(req["status"])))
	switch status {
	case StatusApproved, StatusRejected:
		review, err = h.Service.Review(ctx, reviewID, status)
	case StatusRevoked:
		if !ctx.User.IsAdmin() {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		review, err = h.Service.Revoke(ctx, reviewID)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid status"})
		return
	}

	switch err {
	case ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
	case ErrNotEligible, ErrWrongState:
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
	case nil:
		c.JSON(http.StatusOK, review)
	default:
		log.Errorf("failed processing review, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

func (h *Handler) FindAll(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	reviews, err := h.Service.FindAll(context)
	if err != nil {
		log.Errorf("failed listing reviews, err=%v", err)
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, reviews)
}

func (h *Handler) FindOne(c *gin.Context) {
	context := user.ContextUser(c)
	log := user.ContextLogger(c)

	id := c.Param("id")
	review, err := h.Service.FindOne(context, id)
	if err != nil {
		log.Errorf("failed fetching review %v, err=%v", id, err)
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if review == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	c.PureJSON(http.StatusOK, review)
}

func (h *Handler) RunExec(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	reviewID := c.Param("id")
	review, err := h.Service.FindOne(ctx, reviewID)
	if err != nil {
		log.Errorf("failed retrieving review, err=%v", err)
		c.JSON(http.StatusInternalServerError, &clientexec.ExecErrResponse{Message: "failed retrieving review"})
		return
	}
	if review == nil {
		c.JSON(http.StatusNotFound, &clientexec.ExecErrResponse{Message: "review not found"})
		return
	}
	if review.CreatedBy.Email != ctx.User.Email {
		c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{Message: "only the creator can trigger this action"})
		return
	}
	if review.Status != StatusApproved {
		c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{Message: "review not approved or already executed"})
		return
	}
	// the session declared in the review might no longer exists,
	// if the user ctrl+c the client and run via API later
	// a new session is created to execute fresh
	review.Session = uuid.NewString()

	// avoids running twice the same review
	review.Status = StatusProcessing

	if err := h.Service.Persist(ctx, review); err != nil {
		log.Errorf("failed updating review, err=%v", err)
		c.JSON(http.StatusInternalServerError, &clientexec.ExecErrResponse{Message: "exec failed"})
		return
	}

	client, err := clientexec.New(ctx.Org.Id, getAccessToken(c), review.Connection.Name, review.Session)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"session_id": nil, "message": err.Error()})
		return
	}
	clientResp := make(chan *clientexec.Response)
	go func() {
		defer close(clientResp)
		defer client.Close()
		select {
		case clientResp <- client.Run([]byte(review.Input), nil):
		default:
		}
	}()
	log = log.With("session", client.SessionID())
	log.Infof("review apiexec, reviewid=%v, connection=%v, owner=%v, input-lenght=%v",
		reviewID, review.Connection.Name, review.CreatedBy, len(review.Input))
	c.Header("Location", fmt.Sprintf("/api/plugins/audit/sessions/%s/status", client.SessionID()))
	statusCode := http.StatusOK

	select {
	case resp := <-clientResp:
		review.Status = StatusExecuted
		if err := h.Service.Persist(ctx, review); err != nil {
			log.Warnf("failed updating review to executed status, err=%v", err)
		}
		log.Infof("review exec response. exit_code=%v, truncated=%v, response-length=%v",
			resp.ExitCode, resp.Truncated, len(resp.ErrorMessage()))

		if resp.IsError() {
			c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{
				SessionID: &resp.SessionID,
				Message:   resp.ErrorMessage(),
				ExitCode:  resp.ExitCode,
			})
			return
		}
		c.JSON(statusCode, resp)
	case <-time.After(time.Second * 50):
		log.Infof("review exec timeout (50s), it will return async")
		// closing the client will force the goroutine to end
		// and the result will return async
		client.Close()

		// we do not know the status of this in the future.
		// replaces the current "PROCESSING" status
		review.Status = StatusUnknown
		if err := h.Service.Persist(ctx, review); err != nil {
			log.Warnf("failed updating review to unknown status, err=%v", err)
		}

		c.JSON(http.StatusAccepted, gin.H{"session_id": client.SessionID(), "exit_code": nil})
	}
}

func getAccessToken(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
}
