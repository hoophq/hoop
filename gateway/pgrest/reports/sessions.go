package pgreports

import (
	"errors"
	"fmt"
	"time"

	"github.com/runopsio/hoop/gateway/pgrest"
)

type OptionKey string

type SessionOption struct {
	OptionKey OptionKey
	OptionVal any
}

const (
	OptionGroupBy    OptionKey = "group_by"
	OptionID         OptionKey = "id"
	OptionConnection OptionKey = "connection"
	OptionType       OptionKey = "type"
	OptionVerb       OptionKey = "verb"
	OptionUser       OptionKey = "user"
	OptionStartDate  OptionKey = "start_date"
	OptionEndDate    OptionKey = "end_date"

	GroupByConnection string = "connection_name"
	GroupByID         string = "id"
	GroupByUser       string = "user_email"
	GroupByType       string = "connection_type"

	maxDaysRange float64 = 120 * 24
)

var (
	ErrInvalidDateRange    = errors.New("invalid date range, expected to be between 120 days range")
	ErrInvalidDateFormat   = errors.New("invalid date format, expected format YYYY-MM-DD")
	ErrInvalidGroupByValue = fmt.Errorf("invalid group_by value, expected=%v",
		[]string{GroupByConnection, GroupByID, GroupByType, GroupByUser})
)

func GetSessionReport(ctx pgrest.OrgContext, opts ...*SessionOption) (*pgrest.SessionReport, error) {
	today := time.Now().UTC()
	request := map[OptionKey]any{
		"group_by":        GroupByConnection,
		"id":              "",
		"connection_name": "",
		"connection_type": "",
		"verb":            "",
		"user_email":      "",
		"start_date":      today.Format(time.DateOnly),
		"end_date":        today.AddDate(0, 0, 1).Format(time.DateOnly),
	}
	for _, opt := range opts {
		if _, ok := request[opt.OptionKey]; !ok {
			continue
		}
		if opt.OptionKey == "group_by" {
			switch fmt.Sprintf("%v", opt.OptionVal) {
			case "connection", "id", "user_email", "connection_type":
				break
			default:
				return nil, ErrInvalidGroupByValue
			}
		}
		request[opt.OptionKey] = opt.OptionVal
	}
	t1, t1Err := time.Parse(time.DateOnly, fmt.Sprintf("%v", request["start_date"]))
	t2, t2Err := time.Parse(time.DateOnly, fmt.Sprintf("%v", request["end_date"]))
	if t1Err != nil || t2Err != nil {
		return nil, ErrInvalidDateFormat
	}
	if t2.Sub(t1).Hours() > maxDaysRange {
		return nil, ErrInvalidDateRange
	}

	request["org_id"] = ctx.GetOrgID()
	sessionReport := pgrest.SessionReport{Items: []pgrest.SessionReportItem{}}
	err := pgrest.New("/rpc/session_report").RpcCreate(request).DecodeInto(&sessionReport.Items)
	switch err {
	case pgrest.ErrNotFound:
		return &sessionReport, nil
	default:
		for _, item := range sessionReport.Items {
			sessionReport.TotalRedactCount += item.RedactTotal
			sessionReport.TotalTransformedBytes += item.TransformedBytes
		}
		return &sessionReport, err
	}
}
