package sessionapi

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/storagev2"
	reviewStorage "github.com/runopsio/hoop/gateway/storagev2/review"
	"github.com/runopsio/hoop/gateway/user"
)

func GetById(c *gin.Context) {
	storageCtx := storagev2.ParseContext(c)
	log := user.ContextLogger(c)

	id := c.Param("id")
	review, err := reviewStorage.FindOne(storageCtx, id)
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
