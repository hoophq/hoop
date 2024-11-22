package reviewapi

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgreview "github.com/hoophq/hoop/gateway/pgrest/review"
	"github.com/hoophq/hoop/gateway/review"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type handler struct {
	legacy *review.Handler
}

func NewHandler(legacyHandler *review.Handler) *handler { return &handler{legacyHandler} }

// GetReviewByID
//
//	@Summary		Get Review
//	@Description	Get review resource by id
//	@Tags			Core
//	@Param			id	path	string	true	"Resource identifier of the review"
//	@Produce		json
//	@Success		200		{object}	openapi.Review
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/reviews/{id} [get]
func (h *handler) Get(c *gin.Context) {
	storageCtx := storagev2.ParseContext(c)

	id := c.Param("id")
	review, err := pgreview.New().FetchOneByID(storageCtx, id)
	if err != nil {
		if err == pgrest.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "review not found"})
			return
		}
		log.Errorf("failed fetching review %v, err=%v", id, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if review == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "review not found"})
		return
	}
	c.JSON(http.StatusOK, pgreview.ToJson(*review))
}

// ListReviews
//
//	@Summary		List Reviews
//	@Description	List review resources
//	@Tags			Core
//	@Produce		json
//	@Success		200	{array}		openapi.Review
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/reviews [get]
func (h *handler) List(c *gin.Context) {
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

// UpdateReview
//
//	@Summary		Update Review Status
//	@Description	Update the status of a review resource
//	@Tags			Core
//	@Param			id	path	string	true	"Resource identifier of the review"
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.ReviewRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.Review
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/reviews/{id} [put]
func (h *handler) Put(c *gin.Context) { h.legacy.Put(c) }
