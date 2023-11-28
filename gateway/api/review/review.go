package reviewapi

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	"github.com/runopsio/hoop/gateway/storagev2"
	reviewstorage "github.com/runopsio/hoop/gateway/storagev2/review"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

func GetById(c *gin.Context) {
	storageCtx := storagev2.ParseContext(c)
	log := user.ContextLogger(c)

	id := c.Param("id")
	if pgrest.Rollout {
		review, err := pgreview.New().FetchOneByID(storageCtx, id)
		if err != nil {
			if err == pgrest.ErrNotFound {
				c.JSON(http.StatusNotFound, gin.H{"message": "review not found"})
				return
			}
			log.Errorf("failed fetching review %v, err=%v", id, err)
			sentry.CaptureException(err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.PureJSON(http.StatusOK, pgreview.ToJson(*review))
		return
	}
	review, err := reviewstorage.FindOne(storageCtx, id)
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

	c.PureJSON(http.StatusOK, types.ReviewJSON{
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
	})
}
