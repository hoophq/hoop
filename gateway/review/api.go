package review

import (
	"net/http"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Handler struct {
		Service       service
		PluginService *plugin.Service
	}

	service interface {
		FindAll(context *user.Context) ([]types.Review, error)
		Review(context *user.Context, id string, status types.ReviewStatus) (*types.Review, error)
		Revoke(ctx *user.Context, id string) (*types.Review, error)
		Persist(context *user.Context, review *types.Review) error
	}
)

func (h *Handler) Put(c *gin.Context) {
	ctx := user.ContextUser(c)
	log := user.ContextLogger(c)

	id := c.Param("id")
	var req map[string]string
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	var review *types.Review
	status := types.ReviewStatus(strings.ToUpper(string(req["status"])))
	switch status {
	case types.ReviewStatusApproved, types.ReviewStatusRejected:
		review, err = h.Service.Review(ctx, id, status)
	case types.ReviewStatusRevoked:
		if !ctx.User.IsAdmin() {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		review, err = h.Service.Revoke(ctx, id)
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
		c.JSON(http.StatusOK, sanitizeReview(review))
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

	var reviewsList []types.ReviewJSON
	for _, obj := range reviews {
		reviewsList = append(reviewsList, sanitizeReview(&obj))
	}

	c.JSON(http.StatusOK, reviewsList)
}

func sanitizeReview(review *types.Review) types.ReviewJSON {
	reviewOwnerMap, _ := review.CreatedBy.(map[any]any)
	if reviewOwnerMap == nil {
		reviewOwnerMap = map[any]any{
			edn.Keyword("xt/id"):      "",
			edn.Keyword("user/name"):  "",
			edn.Keyword("user/email"): "",
		}
	}

	reviewConnectionMap, _ := review.ConnectionId.(map[any]any)
	if reviewConnectionMap == nil {
		reviewConnectionMap = map[any]any{
			edn.Keyword("xt/id"):           "",
			edn.Keyword("connection/name"): "",
		}
	}

	reviewOwnerToStringFn := func(key string) string {
		v, _ := reviewOwnerMap[edn.Keyword(key)].(string)
		return v
	}

	connectionToStringFn := func(key string) string {
		v, _ := reviewConnectionMap[edn.Keyword(key)].(string)
		return v
	}

	return types.ReviewJSON{
		Id:              review.Id,
		OrgId:           review.OrgId,
		CreatedAt:       review.CreatedAt,
		Type:            review.Type,
		Session:         review.Session,
		Input:           review.Input,
		InputEnvVars:    review.InputEnvVars,
		InputClientArgs: review.InputClientArgs,
		AccessDuration:  review.AccessDuration,
		Status:          review.Status,
		RevokeAt:        review.RevokeAt,
		ReviewOwner: types.ReviewOwner{
			Id:    reviewOwnerToStringFn("xt/id"),
			Name:  reviewOwnerToStringFn("user/name"),
			Email: reviewOwnerToStringFn("user/email"),
		},
		Connection: types.ReviewConnection{
			Id:   connectionToStringFn("xt/id"),
			Name: connectionToStringFn("connection/name"),
		},
		ReviewGroupsData: review.ReviewGroupsData,
	}
}
