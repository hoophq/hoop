package metrics

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// SessionMetrics
//
//	@Summary		Get Session Metrics
//	@Description	Query session metrics data with advanced filtering. Supports AND/OR logic for combining filters. Filter by resource types (connection_type), resource subtypes (connection_subtype), resources (connection_name), Presidio data types (info_type), masked/unmasked status, date ranges, session dates, and session duration.
//	@Tags			Session Metrics
//	@Accept			json
//	@Produce		json
//	@Param			connection_type			query		[]string	false	"Filter by connection types (e.g., postgres, mysql)"
//	@Param			connection_subtype		query		[]string	false	"Filter by connection subtypes (e.g., amazon-rds, azure-db)"
//	@Param			connection_name			query		[]string	false	"Filter by specific connection names"
//	@Param			info_type				query		[]string	false	"Filter by Presidio data types (e.g., EMAIL_ADDRESS, CREDIT_CARD, PHONE_NUMBER)"
//	@Param			only_masked				query		bool		false	"Only return masked data (count_masked > 0)"
//	@Param			only_unmasked			query		bool		false	"Only return unmasked data (count_masked = 0, but count_analyzed > 0)"
//	@Param			start_date				query		string		false	"Start date for filtering (format: YYYY-MM-DD)"
//	@Param			end_date				query		string		false	"End date for filtering (format: YYYY-MM-DD)"
//	@Param			session_id				query		[]string	false	"Filter by specific session IDs"
//	@Param			session_start_date		query		string		false	"Filter sessions that started on or after this date (format: YYYY-MM-DD)"
//	@Param			session_end_date		query		string		false	"Filter sessions that ended on or before this date (format: YYYY-MM-DD)"
//	@Param			min_duration_sec		query		int			false	"Minimum session duration in seconds"
//	@Param			max_duration_sec		query		int			false	"Maximum session duration in seconds"
//	@Param			include_open_sessions	query		bool		false	"Include sessions that are still open (not ended)"
//	@Param			logic_operator			query		string		false	"Logic operator for combining filters: 'and' or 'or' (default: 'and')"
//	@Param			page					query		int			false	"Pagination page (default: 1)"
//	@Param			limit					query		int			false	"Pagination limit (default: 100, max: 1000)"
//	@Param			aggregated				query		bool		false	"Return aggregated metrics instead of detailed list"
//	@Failure		400,500					{object}	openapi.HTTPError
//	@Router			/metrics/sessions [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	// Parse query parameters
	var filter models.SessionMetricsFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		log.Warnf("failed to bind query parameters: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": "Invalid query parameters: " + err.Error()})
		return
	}

	// Check if aggregated metrics are requested
	aggregated := c.Query("aggregated") == "true"

	if aggregated {
		// Return aggregated metrics
		result, err := models.GetSessionMetricsAggregated(ctx.OrgID, filter)
		if err != nil {
			log.Errorf("failed to get aggregated session metrics: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": "Failed to retrieve session metrics"})
			return
		}

		response := openapi.SessionMetricsAggregatedResponse{
			TotalSessions:       result.TotalSessions,
			UniqueInfoTypes:     result.UniqueInfoTypes,
			TotalMasked:         result.TotalMasked,
			TotalAnalyzed:       result.TotalAnalyzed,
			SessionsWithMasking: result.SessionsWithMasking,
		}
		if result.AvgSessionDurationSec != nil {
			val := int64(*result.AvgSessionDurationSec)
			response.AvgSessionDurationSec = &val
		}

		c.JSON(http.StatusOK, response)
		return
	}

	// Query session metrics
	result, pagination, err := models.GetSessionMetrics(ctx.OrgID, filter)
	if err != nil {
		log.Errorf("failed to query session metrics: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Failed to retrieve session metrics"})
		return
	}

	// Convert to API response format
	items := make([]openapi.SessionMetricResponse, len(result))
	for i, item := range result {
		items[i] = openapi.SessionMetricResponse{
			SessionID:          item.SessionID,
			OrgID:              item.OrgID,
			ConnectionType:     item.ConnectionType,
			ConnectionSubtype:  item.ConnectionSubtype,
			ConnectionName:     item.ConnectionName,
			InfoType:           item.InfoType,
			CountMasked:        item.CountMasked,
			CountAnalyzed:      item.CountAnalyzed,
			IsMasked:           item.IsMasked,
			SessionCreatedAt:   item.SessionCreatedAt,
			SessionEndedAt:     item.SessionEndedAt,
			SessionDurationSec: item.SessionDurationSec,
		}
	}

	response := openapi.PaginatedResponse[openapi.SessionMetricResponse]{
		Data:  items,
		Pages: *pagination,
	}

	c.JSON(http.StatusOK, response)
}
