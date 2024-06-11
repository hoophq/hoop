package apireports

import (
	"net/http"

	"github.com/gin-gonic/gin"
	pgreports "github.com/runopsio/hoop/gateway/pgrest/reports"
	"github.com/runopsio/hoop/gateway/storagev2"
)

func SessionReport(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var opts []*pgreports.SessionOption
	for key, val := range c.Request.URL.Query() {
		opts = append(opts, &pgreports.SessionOption{
			OptionKey: pgreports.OptionKey(key),
			OptionVal: val[0],
		})
	}
	report, err := pgreports.GetSessionReport(ctx, opts...)
	switch err {
	case pgreports.ErrInvalidDateFormat, pgreports.ErrInvalidDateRange, pgreports.ErrInvalidGroupByValue:
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	case nil:
		c.JSON(http.StatusOK, report)
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
}
