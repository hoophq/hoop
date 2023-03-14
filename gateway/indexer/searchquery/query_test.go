package searchquery

import (
	"fmt"
	"testing"

	"github.com/blevesearch/bleve/v2/search/query"
)

func TestParseSimpleMatchQuery(t *testing.T) {
	type wantSpec struct {
		field     string
		match     string
		fuzziness int
	}
	for _, tt := range []struct {
		msg   string
		query string
		want  wantSpec
		err   error
	}{
		{
			msg:   "it must parse query.MatchQuery into input attribute",
			query: "simplequery in:input",
			want:  wantSpec{field: "input", match: "simplequery", fuzziness: 0},
		},
		{
			msg:   "it must parse query.MatchQuery into output attribute",
			query: "simplequery in:output",
			want:  wantSpec{field: "output", match: "simplequery", fuzziness: 0},
		},
		{
			msg:   "it must parse query.MatchQuery with fuzziness",
			query: "simplequery in:output fuzzy:1",
			want:  wantSpec{field: "output", match: "simplequery", fuzziness: 1},
		},
		{
			msg:   "it must parse query.MatchQuery with fuzziness",
			query: "simplequery in:output fuzzy:1",
			want:  wantSpec{field: "output", match: "simplequery", fuzziness: 1},
		},
		{
			msg:   "it must fail when the query operator is in the wrong position",
			query: "simplequery AND in:output",
			err:   fmt.Errorf(`operator "AND" in the wrong position`),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			q, err := Parse("", tt.query)
			if err != nil {
				if tt.err == nil {
					t.Fatalf("expected not to fail on parsing, err=%v", err)
				}
				if fmt.Sprintf("%v", tt.err) != fmt.Sprintf("%v", err) {
					t.Errorf("expected to fail parsing, want=%v, got=%v", tt.err, err)
				}
				return
			}
			queries := booleanToQueryList(q)
			for _, obj := range queries {
				got, _ := obj.(*query.MatchQuery)
				if got == nil {
					t.Fatalf("expected to parse a *query.MatchQuery, found=%T", obj)
				}
				if tt.want.field != got.FieldVal || tt.want.match != got.Match {
					t.Errorf("failed to match field/match, want:%v=%v, found:%v=%v",
						tt.want.field, tt.want.match, got.FieldVal, got.Match)
				}
				if tt.want.fuzziness != got.Fuzziness {
					t.Errorf("failed to match fuzziness, want=%v, found=%v",
						tt.want.fuzziness, got.Fuzziness)
				}
			}

		})
	}
}

func TestParseSimpleWildcardQuery(t *testing.T) {
	type wantSpec struct {
		field    string
		wildcard string
	}
	for _, tt := range []struct {
		msg   string
		query string
		want  wantSpec
		err   error
	}{
		{
			msg:   "it must parse query.WildcardQuery into input attribute",
			query: "AKIA* in:input",
			want:  wantSpec{field: "input", wildcard: "AKIA*"},
		},
		{
			msg:   "it must parse query.WildcardQuery into output attribute",
			query: "inf? in:output",
			want:  wantSpec{field: "output", wildcard: "inf?"},
		},
		{
			msg:   "it must fail when passing wildcard with less than three characters",
			query: "a* in:output",
			err:   errMinRequiredWildcardChar,
		},
		{
			msg:   "it must fail when using more than three query operators with wildcards",
			query: "abc* AND def* AND ghi* AND jkl* in:output",
			err:   errMaxWildcardQueryOperators,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			q, err := Parse("", tt.query)
			if err != nil {
				if tt.err == nil {
					t.Fatalf("expected not to fail on parsing, err=%v", err)
				}
				if fmt.Sprintf("%v", tt.err) != fmt.Sprintf("%v", err) {
					t.Errorf("expected to fail parsing, want=%v, got=%v", tt.err, err)
				}
				return
			}
			queries := booleanToQueryList(q)
			for _, obj := range queries {
				got, _ := obj.(*query.WildcardQuery)
				if got == nil {
					t.Fatalf("expected to parse a *query.MatchQuery, found=%T", obj)
				}
				if tt.want.field != got.FieldVal || tt.want.wildcard != got.Wildcard {
					t.Errorf("failed to match field/wildcard, want:%v=%v, found:%v=%v",
						tt.want.field, tt.want.wildcard, got.FieldVal, got.Wildcard)
				}
			}
		})
	}
}

func TestParseComplexQuery(t *testing.T) {
	type wantSpec struct {
		field     string
		queryTerm string
	}
	for _, tt := range []struct {
		msg   string
		query string
		want  map[string]wantSpec
		err   error
	}{
		{
			msg:   "it must parse to three match query types",
			query: "complex01 AND complex02 AND complex03 in:output",
			want: map[string]wantSpec{
				"complex01": {field: "output", queryTerm: "complex01"},
				"complex02": {field: "output", queryTerm: "complex02"},
				"complex03": {field: "output", queryTerm: "complex03"},
			},
		},
		{
			msg:   "it must parse to three wildcard query types",
			query: "com* AND compl* AND comple? in:output",
			want: map[string]wantSpec{
				"com*":    {field: "output", queryTerm: "com*"},
				"compl*":  {field: "output", queryTerm: "compl*"},
				"comple?": {field: "output", queryTerm: "comple?"},
			},
		},
		{
			msg:   "it must parse mix of wildcard and match query types",
			query: "com* AND aws OR debug NOT info in:output",
			want: map[string]wantSpec{
				"com*":  {field: "output", queryTerm: "com*"},
				"aws":   {field: "output", queryTerm: "aws"},
				"debug": {field: "output", queryTerm: "debug"},
				"info":  {field: "output", queryTerm: "info"},
			},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			q, err := Parse("", tt.query)
			if err != nil {
				if tt.err == nil {
					t.Fatalf("expected not to fail on parsing, err=%v", err)
				}
				if fmt.Sprintf("%v", tt.err) != fmt.Sprintf("%v", err) {
					t.Errorf("expected to fail parsing, want=%v, got=%v", tt.err, err)
				}
				return
			}
			queries := booleanToQueryList(q)
			for _, obj := range queries {
				if got, ok := obj.(*query.WildcardQuery); ok {
					want, ok := tt.want[got.Wildcard]
					if !ok {
						t.Fatalf("expected to find query term=%q", got.Wildcard)
					}
					if want.field != got.FieldVal || want.queryTerm != got.Wildcard {
						t.Errorf("failed to match field/wildcard, want:%v/%v, got:%v/%v",
							want.field, want.queryTerm, got.FieldVal, got.Wildcard)
					}
					continue
				}
				if got, ok := obj.(*query.MatchQuery); ok {
					want, ok := tt.want[got.Match]
					if !ok {
						t.Fatalf("expected to find query term=%q", got.Match)
					}
					if want.field != got.FieldVal || want.queryTerm != got.Match {
						t.Errorf("failed to match field/match, want:%v/%v, got:%v/%v",
							want.field, want.queryTerm, got.FieldVal, got.Match)
					}
					continue
				}
				t.Fatalf("it did not match any known type, found=%T", obj)
			}
		})
	}
}
