package searchquery

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

const dateFormat = "2006-01-02"

var (
	// using wildcards must have at least 3 characters to avoid extra memory consumption
	reIsValidWildcardQuery = regexp.MustCompile(`[^*]{3,}[\*|\?]+`).MatchString
	reHasNumericOperator   = regexp.MustCompile(`[<>]+[0-9]+$|^[0-9]+\.\.+[0-9]+$`).MatchString
	reHasDateOperator      = regexp.MustCompile(`[<>][0-9-]{10}T[0-9:Z]{9}$|[<>][0-9-]{10}$|[0-9-]{10}\.\.[0-9-]{10}$|[0-9-]{10}T[0-9:Z]{9}\.\.[0-9-]{10}T[0-9:Z]{9}$|\-\d{0,3}[h|m|s]$`).
				MatchString
)

type qualifier struct {
	attribute     string
	value         string
	mustNot       bool
	isQueryOption bool
}

func newQualifier(qualifierStr string) (*qualifier, error) {
	attr, val, found := strings.Cut(qualifierStr, ":")
	if !found {
		return nil, fmt.Errorf("possible unrecognized qualifier: %q", qualifierStr)
	}
	if len(val) == 0 {
		return nil, errMissingQualifierVal
	}
	q := &qualifier{attribute: attr, value: val}
	if attr[0] == '-' {
		q.mustNot = true
		q.attribute = attr[1:]
	}
	if _, ok := registeredQualifiers[q.attribute]; !ok {
		return nil, fmt.Errorf("qualifier not found: %v", q.attribute)
	}
	switch q.attribute {
	case QualifierQueryIn:
		if val != QualifierQueryInInput && val != QualifierQueryInOutput {
			return nil, fmt.Errorf(`'in' qualifier value %q doesn't exists`, val)
		}
		q.isQueryOption = true
	case QualifierBoolFilterIs:
		if val != QualifierBoolTruncated && val != QualifierBoolError {
			return nil, fmt.Errorf(`'is' qualifier value %q doesn't exists`, val)
		}
		if val == QualifierBoolTruncated {
			q.isQueryOption = true
		}
	}
	return q, nil
}

func (q *qualifier) int() int {
	return parseInt(q.value)
}

// connection:name user:foobar
func (q *qualifier) parseQualifierFilter() (filter query.Query, err error) {
	if q == nil {
		return
	}
	switch q.attribute {
	case QualifierFilterUser, QualifierFilterConnection, QualifierFilterConnectionType,
		QualifierFilterVerb, QualifierFilterSession:
		filter = &query.TermQuery{
			Term:     q.value,
			FieldVal: q.attribute,
		}
	case QualifierFilterDuration, QualifierFilterSize:
		min, max, err := parseNumericOperator(q.value)
		if err != nil {
			return nil, err
		}
		nq := bleve.NewNumericRangeQuery(pfloat64(min, false), pfloat64(max, true))
		nq.SetField(q.attribute)
		filter = nq
	case QualifierFilterStartDate, QualifierFilterCompleteDate:
		start, end, err := parseDateOperator(q.value)
		if err != nil {
			return nil, err
		}
		dq := bleve.NewDateRangeQuery(start, end)
		dq.SetField(q.attribute)
		filter = dq
	case QualifierBoolFilterIs:
		filter = &query.BoolFieldQuery{
			Bool:     true,
			FieldVal: q.value,
		}
	}
	return
}

// parseNumericOperator parses numeric fields based in the expression bellow
//
// greater than: >10
// less than: <10
// in between: 10..20
func parseNumericOperator(fieldVal string) (min int, max int, err error) {
	if !reHasNumericOperator(fieldVal) {
		return 0, 0, fmt.Errorf("invalid numeric operator: %v", fieldVal)
	}
	operator := fieldVal[0]
	switch operator {
	case '>':
		min = parseInt(fieldVal[1:])
	case '<':
		max = parseInt(fieldVal[1:])
	default:
		mi, mx, _ := strings.Cut(fieldVal, "..")
		min, max = parseInt(mi), parseInt(mx)
		if min >= max {
			return 0, 0, fmt.Errorf("min must be less than max")
		}
	}
	return
}

func pfloat64(v int, zeroToNil bool) *float64 {
	if zeroToNil && v == 0 {
		return nil
	}
	f := float64(v)
	return &f
}

func parseInt(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}

func parseDateOperator(fieldVal string) (start, end time.Time, err error) {
	if !reHasDateOperator(fieldVal) {
		err = fmt.Errorf("invalid date operator: %v", fieldVal)
		return
	}
	operator := fieldVal[0]
	switch operator {
	case '>':
		start, err = parseDateString(fieldVal[1:])
	case '<':
		end, err = parseDateString(fieldVal[1:])
	case '-':
		var dur time.Duration
		dur, err = time.ParseDuration(fieldVal)
		if err != nil {
			return
		}
		start = time.Now().UTC().Add(dur)
	default:
		s, e, found := strings.Cut(fieldVal, "..")
		if !found {
			err = fmt.Errorf("parse error for date time range, field %v", fieldVal)
			return
		}
		start, err = parseDateString(s)
		if err != nil {
			return
		}
		end, err = parseDateString(e)
	}
	return
}

// parseDateString parse to a date format YYYY-MM-DD or RFC3339
func parseDateString(t string) (time.Time, error) {
	// 2022-02-10
	if len(t) == 10 {
		return time.ParseInLocation(dateFormat, t, time.UTC)
	}
	return time.Parse(time.RFC3339, t)
}

func isQueryOperator(val string) bool {
	v := Operator(val)
	return v == QueryOperatorAND || v == QueryOperatorOR || v == QueryOperatorNOT
}
