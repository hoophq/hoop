package reviewapi

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	"github.com/runopsio/hoop/gateway/storagev2"
)

func GetById(c *gin.Context) {
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
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.PureJSON(http.StatusOK, pgreview.ToJson(*review))
}
