package apireports

import (
	"net/http"

	"github.com/gin-gonic/gin"
	pgreports "github.com/hoophq/hoop/gateway/pgrest/reports"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// Session Reports
// TODO: refactor to use types from openapi package
//
//	@Summary		Session Reports
//	@Description	The report payload groups sessions by info types and by a custom field (`group_by`) provided by the client.
//	@Description	The items returns data containing the sum of redact fields performed by a given info type aggregated by the `group_by` attribute.
//	@Tags			Core
//	@Produce		json
//	@Param			params	query		openapi.SessionReportParams	false	"-"
//	@Success		200		{object}	openapi.SessionReport
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/reports/sessions [get]
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
