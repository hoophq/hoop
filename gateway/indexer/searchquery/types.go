package searchquery

import (
	"errors"
	"fmt"
)

type Operator string

const (
	QueryOperatorOR  Operator = "OR"
	QueryOperatorAND Operator = "AND"
	QueryOperatorNOT Operator = "NOT"

	QualifierBoolFilterIs        = "is"
	QualifierBoolTruncated       = "truncated"
	QualifierBoolError           = "error"
	QualifierBoolInputTruncated  = "isinput_trunc"
	QualifierBoolOutputTruncated = "isoutput_trunc"

	QualifierQueryFuzzy    = "fuzzy"
	QualifierQueryIn       = "in"
	QualifierQueryInInput  = "input"
	QualifierQueryInOutput = "output"

	QualifierFilterConnection     = "connection"
	QualifierFilterConnectionType = "connection_type"
	QualifierFilterSession        = "session"
	QualifierFilterUser           = "user"
	QualifierFilterVerb           = "verb"
	QualifierFilterSize           = "size"
	QualifierFilterDuration       = "duration"
	QualifierFilterStartDate      = "started"
	QualifierFilterCompleteDate   = "completed"
)

var (
	errMinRequiredWildcardChar   = errors.New("wildcard queries must have at least 3 characters")
	errMaxWildcardQueryOperators = fmt.Errorf("reached max (3) of wildcard query operators in a query")
	errMissingQualifierVal       = errors.New("missing qualifier value")
)

var registeredQualifiers = map[string]any{
	QualifierQueryIn:              nil,
	QualifierBoolFilterIs:         nil,
	QualifierFilterConnection:     nil,
	QualifierFilterConnectionType: nil,
	QualifierFilterSession:        nil,
	QualifierFilterUser:           nil,
	QualifierFilterVerb:           nil,
	QualifierFilterSize:           nil,
	QualifierFilterDuration:       nil,
	QualifierFilterStartDate:      nil,
	QualifierFilterCompleteDate:   nil,

	QualifierQueryFuzzy: nil,
}
