package indexer

import "github.com/runopsio/hoop/gateway/indexer/searchquery"

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
