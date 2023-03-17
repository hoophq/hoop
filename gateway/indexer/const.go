package indexer

import (
	"time"

	"github.com/runopsio/hoop/gateway/indexer/searchquery"
)

const (
	MaxIndexSize       = 600000           // 600KB
	defaultIndexPeriod = time.Hour * 1080 // 45 days
	maxSearchLimit     = 50
)

var registeredFields = []string{
	searchquery.QualifierQueryInInput, searchquery.QualifierQueryInOutput,
	searchquery.QualifierFilterConnection, searchquery.QualifierFilterConnectionType,
	searchquery.QualifierFilterUser, searchquery.QualifierFilterVerb,
	searchquery.QualifierFilterSize, searchquery.QualifierFilterDuration,
	searchquery.QualifierFilterStartDate, searchquery.QualifierFilterCompleteDate,
}

var defaultFields = []string{
	searchquery.QualifierFilterConnection, searchquery.QualifierFilterConnectionType,
	searchquery.QualifierFilterUser, searchquery.QualifierFilterVerb,
	searchquery.QualifierFilterSize, searchquery.QualifierFilterDuration,
	searchquery.QualifierFilterStartDate, searchquery.QualifierFilterCompleteDate,
}
