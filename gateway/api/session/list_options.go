package sessionapi

import (
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

// invalidListOptionError is a query string the caller can fix. The handler
// renders it as 422; anything else coming out of the list path is a 500.
type invalidListOptionError struct {
	option openapi.SessionOptionKey
	reason string
}

func (e *invalidListOptionError) Error() string {
	return fmt.Sprintf("failed listing sessions, invalid %q option: %s", e.option, e.reason)
}

func invalidOption(key openapi.SessionOptionKey, format string, args ...any) error {
	return &invalidListOptionError{option: key, reason: fmt.Sprintf(format, args...)}
}

// parseSessionListOptions builds the model option set for GET /api/sessions
// from a query string.
//
// It is a plain function over url.Values rather than gin.Context so the
// validation rules can be tested without a request, and so every caller of the
// list model gets the same bounds. Unparseable or out-of-range pagination is
// rejected instead of being silently coerced: discarding the strconv error made
// ?limit=abc mean limit=0, which returned an empty page while still reporting
// has_next_page=true.
func parseSessionListOptions(qs url.Values) (models.SessionOption, error) {
	option := models.NewSessionOption()
	for _, optKey := range openapi.AvailableSessionOptions {
		values, ok := qs[string(optKey)]
		if !ok || len(values) == 0 {
			continue
		}
		queryOptVal := values[0]
		switch optKey {
		case openapi.SessionOptionUser:
			option.User = queryOptVal
		case openapi.SessionOptionConnection:
			option.ConnectionName = queryOptVal
		case openapi.SessionOptionType:
			option.ConnectionType = queryOptVal
		case openapi.SessionOptionReviewStatus:
			option.ReviewStatus = queryOptVal
		case openapi.SessionOptionReviewApproverEmail:
			option.ReviewApproverEmail = &queryOptVal
		case openapi.SessionOptionBatchID:
			option.BatchID = &queryOptVal
		case openapi.SessionOptionCorrelationID:
			if queryOptVal != "" {
				option.CorrelationID = &queryOptVal
			}
		case openapi.SessionOptionJiraIssueKey:
			keys := strings.Split(queryOptVal, ",")
			for i, k := range keys {
				keys[i] = strings.ToLower(strings.TrimSpace(k))
			}
			option.JiraIssueKey = keys
		case openapi.SessionOptionStartDate:
			optTimeVal, err := time.Parse(time.RFC3339, queryOptVal)
			if err != nil {
				return option, invalidOption(optKey, "start_date or end_date in wrong format")
			}
			option.StartDate = sql.NullString{String: optTimeVal.Format(time.RFC3339), Valid: true}
		case openapi.SessionOptionEndDate:
			optTimeVal, err := time.Parse(time.RFC3339, queryOptVal)
			if err != nil {
				return option, invalidOption(optKey, "start_date or end_date in wrong format")
			}
			option.EndDate = sql.NullString{String: optTimeVal.Format(time.RFC3339), Valid: true}
		case openapi.SessionOptionLimit:
			limit, err := strconv.Atoi(queryOptVal)
			if err != nil {
				return option, invalidOption(optKey, "%q is not a number", queryOptVal)
			}
			if limit < 1 {
				return option, invalidOption(optKey, "must be at least 1, got %d", limit)
			}
			// The upper bound is clamped rather than rejected: asking for more
			// than a page holds has always returned the maximum page, and
			// turning that into an error would break existing clients.
			option.Limit = min(limit, models.MaxSessionListLimit)
		case openapi.SessionOptionOffset:
			offset, err := strconv.Atoi(queryOptVal)
			if err != nil {
				return option, invalidOption(optKey, "%q is not a number", queryOptVal)
			}
			if offset < 0 {
				return option, invalidOption(optKey, "must not be negative, got %d", offset)
			}
			// Rejected rather than clamped, because clamping would silently
			// serve a different page than the one requested. An offset this
			// deep is expensive however the query is written; narrow the result
			// set with a date or connection filter instead.
			if offset > models.MaxSessionListOffset {
				return option, invalidOption(optKey,
					"must not be greater than %d, got %d; narrow the result set with a filter instead of paginating deeper",
					models.MaxSessionListOffset, offset)
			}
			option.Offset = offset
		case openapi.SessionOptionCount:
			countMode, err := models.ParseSessionCountMode(queryOptVal)
			if err != nil {
				return option, invalidOption(optKey, "%v", err)
			}
			option.CountMode = countMode
		}
	}

	if option.StartDate.Valid && !option.EndDate.Valid {
		option.EndDate = sql.NullString{String: time.Now().UTC().Format(time.RFC3339), Valid: true}
	}
	return option, nil
}
