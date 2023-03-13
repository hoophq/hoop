package searchquery

import (
	"fmt"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

// Parse parses a query expression to a bleve query.Query
// it follows the syntax of
//
// SEARCH_KEYWORD_1 SEARCH_KEYWORD_N QUALIFIER_1 QUALIFIER_N
// Example:
// dwarf kingdom in:input connection:postgres
func Parse(scopeUserID, queryString string) (query.Query, error) {
	sq := &searchQuery{scopeUser: scopeUserID}
	var query []string
	for _, keyword := range strings.Split(queryString, " ") {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		if isQueryOperator(keyword) {
			if len(query) == 0 {
				sq.queries = append(sq.queries, keyword)
				continue
			}
			sq.queries = append(sq.queries, strings.Join(query, " "))
			sq.queries = append(sq.queries, keyword)
			query = []string{}
			continue
		}

		// qualifier:val
		if strings.Contains(keyword, ":") {
			qualifier, err := newQualifier(keyword)
			if err != nil {
				return nil, err
			}
			sq.add(qualifier)
			continue
		}
		query = append(query, keyword)
	}
	if len(query) > 0 {
		sq.queries = append(sq.queries, strings.Join(query, " "))
	}
	if len(sq.queries) > 0 {
		if val := sq.queries[len(sq.queries)-1]; isQueryOperator(val) {
			return nil, fmt.Errorf("operator %q in the wrong position", val)
		}
	}
	return sq.Parse()
}

type searchQuery struct {
	items     []*qualifier
	queries   []string
	scopeUser string
}

func (s *searchQuery) Parse() (query.Query, error) {
	q := bleve.NewBooleanQuery()
	if s.scopeUser != "" {
		q.AddMust(&query.TermQuery{
			Term:     s.scopeUser,
			FieldVal: QualifierFilterUser,
		})
	}
	// is:truncated in:input|output
	isQualifierTrunc, inQualifier := s.isQualifierTruncated(), s.inQualifier()
	if inQualifier != nil && isQualifierTrunc != nil {
		fieldVal := QualifierBoolInputTruncated
		if inQualifier.value == QualifierQueryInOutput {
			fieldVal = QualifierBoolOutputTruncated
		}
		q.AddMust(&query.BoolFieldQuery{
			Bool:     true,
			FieldVal: fieldVal,
		})
	}

	// qualifiers
	for _, entry := range s.items {
		qq, err := entry.parseQualifierFilter()
		if err != nil {
			return nil, err
		}
		if qq == nil || entry.isQueryOption {
			continue
		}
		if entry.mustNot {
			q.AddMustNot(qq)
			continue
		}
		q.AddMust(qq)
	}

	wildcardQueryCount := 0
	for i, queryVal := range s.queries {
		if isQueryOperator(queryVal) {
			continue
		}
		if reIsValidWildcardQuery(queryVal) {
			wildcardQueryCount++
		}
		if i == 0 {
			if err := s.setQueryString(queryVal, QueryOperatorOR, q); err != nil {
				return nil, err
			}
			continue
		}
		lastOperator := Operator(s.queries[i-1])
		if err := s.setQueryString(queryVal, lastOperator, q); err != nil {
			return nil, err
		}
	}
	if wildcardQueryCount > 3 {
		return nil, errMaxWildcardQueryOperators
	}
	return q, nil
}

// setQueryString adds query base in the condition of the query operator.
// The query is only added if the in qualifier is present, e.g.: in:input|output
//
// if the query string has spaces a Match Phrase query is added, otherwise
// it will use a Match Query
func (s *searchQuery) setQueryString(queryStr string, operator Operator, bq *query.BooleanQuery) (err error) {
	var outcome query.Query
	inQualifier := s.inQualifier()
	if inQualifier == nil {
		return
	}
	matchq := bleve.NewMatchQuery(queryStr)
	matchq.SetField(inQualifier.value)
	if fuzzy := s.fuzzyQualifier(); fuzzy != nil {
		matchq.SetFuzziness(fuzzy.int())
	}
	outcome = matchq
	if strings.Contains(queryStr, "*") || strings.Contains(queryStr, "?") {
		if !reIsValidWildcardQuery(queryStr) {
			return errMinRequiredWildcardChar
		}
		wq := bleve.NewWildcardQuery(queryStr)
		wq.SetField(inQualifier.value)
		outcome = wq
	}
	switch operator {
	case QueryOperatorAND:
		bq.AddMust(outcome)
	case QueryOperatorOR:
		bq.AddShould(outcome)
	case QueryOperatorNOT:
		bq.AddMustNot(outcome)
	}
	return
}

func (s *searchQuery) add(q *qualifier) {
	s.items = append(s.items, q)
}

// inQualifier lookup for the "in" qualifier
func (s *searchQuery) inQualifier() *qualifier {
	return s.lookupByAttr(QualifierQueryIn)
}

// fuzzyQualifier lookup for the "fuzzy" qualifier
func (s *searchQuery) fuzzyQualifier() *qualifier {
	return s.lookupByAttr(QualifierQueryFuzzy)
}

// isQualifierTruncated, returns the is:truncated qualifier
func (s *searchQuery) isQualifierTruncated() *qualifier {
	return s.lookup(QualifierBoolFilterIs, QualifierBoolTruncated)
}

func (s *searchQuery) lookupByAttr(attribute string) *qualifier {
	for _, entry := range s.items {
		if entry.attribute == attribute {
			return entry
		}
	}
	return nil
}

// lookup for a qualifier:val
func (s *searchQuery) lookup(attribute, val string) *qualifier {
	for _, entry := range s.items {
		if entry.attribute == attribute && entry.value == val {
			return entry
		}
	}
	return nil
}
