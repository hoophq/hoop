package review

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		Review(ctx *storagev2.Context, id string, status types.ReviewStatus) (*types.Review, error)
		ReviewBySid(ctx *storagev2.Context, sid string, status types.ReviewStatus) (*types.Review, error)
		Revoke(ctx pgrest.OrgContext, id string) (*types.Review, error)
		RevokeBySid(ctx pgrest.OrgContext, sid string) (*types.Review, error)
		Persist(ctx pgrest.OrgContext, review *types.Review) error
	}
)

func (h *Handler) Put(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	id, isReviewBySid := c.Param("id"), false
	if sid := c.Param("session_id"); sid != "" {
		id = sid
		isReviewBySid = true
		apiroutes.SetSidSpanAttr(c, sid)
	}
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
		if isReviewBySid {
			review, err = h.Service.ReviewBySid(ctx, id, status)
			break
		}
		review, err = h.Service.Review(ctx, id, status)
	case types.ReviewStatusRevoked:
		if !ctx.IsAdmin() {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if isReviewBySid {
			review, err = h.Service.RevokeBySid(ctx, id)
			break
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
	ctx := storagev2.ParseContext(c)
	reviews, err := pgreview.New().FetchAll(ctx)
	if err != nil && err != pgrest.ErrNotFound {
		log.Errorf("failed listing reviews, err=%v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	var reviewList []types.ReviewJSON
	for _, obj := range reviews {
		reviewList = append(reviewList, *pgreview.ToJson(obj))
	}
	c.JSON(http.StatusOK, reviewList)
}

func sanitizeReview(review *types.Review) types.ReviewJSON {
	reviewOwnerMap, _ := review.CreatedBy.(map[any]any)
	if reviewOwnerMap == nil {
		reviewOwnerMap = map[any]any{
			edn.Keyword("xt/id"):         "",
			edn.Keyword("user/name"):     "",
			edn.Keyword("user/email"):    "",
			edn.Keyword("user/slack-id"): "",
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
		Id:        review.Id,
		OrgId:     review.OrgId,
		CreatedAt: review.CreatedAt,
		Type:      review.Type,
		Session:   review.Session,
		Input:     review.Input,
		// Redacted for now
		// InputEnvVars:    review.InputEnvVars,
		InputClientArgs: review.InputClientArgs,
		AccessDuration:  review.AccessDuration,
		Status:          review.Status,
		RevokeAt:        review.RevokeAt,
		ReviewOwner: types.ReviewOwner{
			Id:      reviewOwnerToStringFn("xt/id"),
			Name:    reviewOwnerToStringFn("user/name"),
			Email:   reviewOwnerToStringFn("user/email"),
			SlackID: reviewOwnerToStringFn("user/slack-id"),
		},
		Connection: types.ReviewConnection{
			Id:   connectionToStringFn("xt/id"),
			Name: connectionToStringFn("connection/name"),
		},
		ReviewGroupsData: review.ReviewGroupsData,
	}
}
