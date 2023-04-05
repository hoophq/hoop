package review

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/clientexec"
	"net/http"
	"strings"
	"time"

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
		FindAll(context *user.Context) ([]Review, error)
		FindOne(context *user.Context, id string) (*Review, error)
		Review(context *user.Context, existingReview *Review, status Status) error
		Persist(context *user.Context, review *Review) error
	}
)

func (h *Handler) Put(c *gin.Context) {
	context := user.ContextUser(c)

	reviewId := c.Param("id")
	existingReview, err := h.Service.FindOne(context, reviewId)
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if existingReview == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	if existingReview.Status != StatusPending {
		c.JSON(http.StatusBadRequest, gin.H{"message": "review must be at PENDING status"})
		return
	}

	isEligibleReviewer := false
	for _, r := range existingReview.ReviewGroups {
		if pb.IsInList(r.Group, context.User.Groups) {
			isEligibleReviewer = true
			break
		}
	}

	if !isEligibleReviewer {
		c.JSON(http.StatusBadRequest, gin.H{"message": "not eligible for review"})
		return
	}

	var r Review
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

	if err := h.Service.Review(context, existingReview, r.Status); err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, existingReview)
}

func (h *Handler) FindAll(c *gin.Context) {
	context := user.ContextUser(c)

	reviews, err := h.Service.FindAll(context)
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, reviews)
}

func (h *Handler) FindOne(c *gin.Context) {
	context := user.ContextUser(c)

	id := c.Param("id")
	review, err := h.Service.FindOne(context, id)
	if err != nil {
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
	if review.Status != StatusApproved {
		c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{Message: "review not approved or already executed"})
		return
	}
	// the session declared in the review might no longer exists,
	// if the user ctrl+c the client and run via API later
	// a new session is created to execute fresh
	review.Session = uuid.NewString()
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
	log.With("session", client.SessionID()).Infof("api exec, connection=%v", review.Connection.Name)
	c.Header("Location", fmt.Sprintf("/api/plugins/audit/sessions/%s/status", client.SessionID()))
	statusCode := http.StatusOK

	select {
	case resp := <-clientResp:
		if resp.IsError() {
			c.JSON(http.StatusBadRequest, &clientexec.ExecErrResponse{
				SessionID: &resp.SessionID,
				Message:   resp.ErrorMessage(),
				ExitCode:  resp.ExitCode,
			})
			return
		}

		review.Status = StatusExecuted
		h.Service.Persist(ctx, review)
		c.JSON(statusCode, resp)
	case <-time.After(time.Second * 50):
		// closing the client will force the goroutine to end
		// and the result will return async
		client.Close()
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
