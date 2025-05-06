package apireports

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const (
	GroupByID         string = "id"
	GroupByUser       string = "user_email"
	GroupByConnection string = "connection_name"
	GroupByType       string = "connection_type"

	maxDaysRange float64 = 120 * 24
)

var (
	ErrInvalidDateRange    = errors.New("invalid date range, expected to be between 120 days range")
	ErrInvalidDateFormat   = errors.New("invalid date format, expected format YYYY-MM-DD")
	ErrInvalidGroupByValue = fmt.Errorf("invalid group_by value, expected=%v",
		[]string{GroupByConnection, GroupByID, GroupByType, GroupByUser})
)

// Session Reports
//
//	@Summary		Session Reports
//	@Description	The report payload groups sessions by info types and by a custom field (`group_by`) provided by the client.
//	@Description	The items returns data containing the sum of redact fields performed by a given info type aggregated by the `group_by` attribute.
//	@Tags			Reports
//	@Produce		json
//	@Param			params	query		openapi.SessionReportParams	false	"-"
//	@Success		200		{object}	openapi.SessionReport
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/reports/sessions [get]
func SessionReport(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	today := time.Now().UTC()
	opts := map[string]any{
		"group_by":        GroupByConnection,
		"id":              "",
		"connection_name": "",
		"connection_type": "",
		"verb":            "",
		"user_email":      "",
		"start_date":      today.Format(time.DateOnly),
		"end_date":        today.AddDate(0, 0, 1).Format(time.DateOnly),
	}
	for key, val := range c.Request.URL.Query() {
		if _, ok := opts[key]; !ok {
			continue
		}

		if key == "group_by" {
			switch val[0] {
			case "connection", "id", "user_email", "connection_type":
				break
			default:
				c.JSON(http.StatusBadRequest, gin.H{"message": ErrInvalidGroupByValue.Error()})
				return
			}
		}
		opts[key] = val[0]
	}

	t1, t1Err := time.Parse(time.DateOnly, fmt.Sprintf("%v", opts["start_date"]))
	t2, t2Err := time.Parse(time.DateOnly, fmt.Sprintf("%v", opts["end_date"]))
	if t1Err != nil || t2Err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": ErrInvalidDateFormat.Error()})
		return
	}
	if t2.Sub(t1).Hours() > maxDaysRange {
		c.JSON(http.StatusBadRequest, gin.H{"message": ErrInvalidDateRange.Error()})
		return
	}

	report, err := models.GetSessionReport(ctx.OrgID, opts)
	if err != nil {
		log.Errorf("failed getting report, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toOpenAPI(report))
}

func toOpenAPI(obj *models.SessionReport) openapi.SessionReport {
	items := []openapi.SessionReportItem{}
	for _, item := range obj.Items {
		items = append(items, openapi.SessionReportItem{
			ResourceName:     item.ResourceName,
			InfoType:         item.InfoType,
			RedactTotal:      item.RedactTotal,
			TransformedBytes: item.TransformedBytes,
		})
	}
	return openapi.SessionReport{
		TotalRedactCount:      obj.TotalRedactCount,
		TotalTransformedBytes: obj.TotalTransformedBytes,
		Items:                 items,
	}
}
