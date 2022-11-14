package review

import (
	pb "github.com/runopsio/hoop/common/proto"
	"net/http"
	"strings"

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
	}
)

func (h *Handler) Put(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	reviewId := c.Param("id")
	existingReview, err := h.Service.FindOne(context, reviewId)
	if err != nil {
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
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, existingReview)
}

func (h *Handler) FindAll(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	reviews, err := h.Service.FindAll(context)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, reviews)
}

func (a *Handler) FindOne(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*user.Context)

	id := c.Param("id")
	review, err := a.Service.FindOne(context, id)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if review == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	c.PureJSON(http.StatusOK, review)
}
