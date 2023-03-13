package searchquery

import (
	"fmt"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2/search/query"
)

func deterministicDate(year int, month time.Month, day, hour int) query.BleveQueryTime {
	return query.BleveQueryTime{time.Date(year, month, day, hour, 0, 0, 0, time.UTC)}
}

func booleanToQueryList(q query.Query) []query.Query {
	bq, _ := q.(*query.BooleanQuery)
	var queries []query.Query
	if must, ok := bq.Must.(*query.ConjunctionQuery); ok {
		queries = append(queries, must.Conjuncts...)
	}
	if should, ok := bq.Should.(*query.DisjunctionQuery); ok {
		queries = append(queries, should.Disjuncts...)
	}
	if mustNot, ok := bq.MustNot.(*query.DisjunctionQuery); ok {
		queries = append(queries, mustNot.Disjuncts...)
	}
	return queries
}

func TestParseQualifier(t *testing.T) {
	for _, tt := range []struct {
		msg        string
		query      string
		want       map[string]*query.TermQuery
		scopedUser string
		err        error
	}{
		{
			msg:   "it must parse filters connection:bash verb:exec",
			query: "connection:bash verb:exec",
			want: map[string]*query.TermQuery{
				"connection": {FieldVal: "connection", Term: "bash"},
				"verb":       {FieldVal: "verb", Term: "exec"},
			},
		},
		{
			msg:   "it must parse filters connection_type:postgres -user:johndoe@corp.tld",
			query: "connection_type:postgres -user:johndoe@corp.tld",
			want: map[string]*query.TermQuery{
				"connection_type": {FieldVal: "connection_type", Term: "postgres"},
				"user":            {FieldVal: "user", Term: "johndoe@corp.tld"},
			},
		},
		{
			msg:   "it must add a qualifier binding the scope to the provided user",
			query: "connection:bash",
			want: map[string]*query.TermQuery{
				"connection": {FieldVal: "connection", Term: "bash"},
				"user":       {FieldVal: "user", Term: "johndoe@corp.tld"},
			},
			scopedUser: "johndoe@corp.tld",
		},
		{
			msg:   "it must return an error when qualifier doesn't have a value",
			query: "connection:",
			err:   errMissingQualifierVal,
		},
		{
			msg:   "it must return an error when passing unknown qualifiers",
			query: "unknown:value",
			err:   fmt.Errorf("qualifier not found: unknown"),
		},
		{
			msg:   "it must return an error when passing unknown value is-qualifier",
			query: "is:unknownval",
			err:   fmt.Errorf(`'is' qualifier value "unknownval" doesn't exists`),
		},
		{
			msg:   "it must return an error when passing unknown value in-qualifier",
			query: "in:unknownval",
			err:   fmt.Errorf(`'in' qualifier value "unknownval" doesn't exists`),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			q, err := Parse(tt.scopedUser, tt.query)
			if err != nil {
				if tt.err == nil {
					t.Fatalf("expected not to fail on parsing, err=%v", err)
				}
				if fmt.Sprintf("%v", tt.err) != fmt.Sprintf("%v", err) {
					t.Errorf("expected to fail parsing, want=%v, got=%v", tt.err, err)
				}
				return
			}
			filters := booleanToQueryList(q)
			for _, obj := range filters {
				got, _ := obj.(*query.TermQuery)
				if got == nil {
					t.Fatalf("expected to parse a *query.TermQuery, found=%T", obj)
				}
				want, ok := tt.want[got.FieldVal]
				if !ok {
					t.Fatalf("expected to find wanted field=%v", got.FieldVal)
				}
				if want.Term != got.Term || want.FieldVal != got.FieldVal {
					t.Errorf("failed to match filters, want:%v=%v, found:%v=%v",
						want.FieldVal, want.Term, got.FieldVal, got.Term)
				}
			}

		})
	}
}

func TestParseNumericQualifiers(t *testing.T) {
	toString := func(v *float64) string {
		if v == nil {
			return "<nil>"
		}
		return fmt.Sprintf("%v", *v)
	}
	for _, tt := range []struct {
		msg   string
		query string
		want  map[string]*query.NumericRangeQuery
		err   error
	}{
		{
			msg:   "it must parse greater than numeric range",
			query: "size:>5",
			want: map[string]*query.NumericRangeQuery{
				"size": {FieldVal: "size", Min: pfloat64(5, false), Max: nil},
			},
		},
		{
			msg:   "it must parse lesser than numeric range",
			query: "size:<100",
			want: map[string]*query.NumericRangeQuery{
				"size": {FieldVal: "size", Min: pfloat64(0, false), Max: pfloat64(100, false)},
			},
		},
		{
			msg:   "it must parse in between numeric range",
			query: "size:10..100",
			want: map[string]*query.NumericRangeQuery{
				"size": {FieldVal: "size", Min: pfloat64(10, false), Max: pfloat64(100, false)},
			},
		},
		{
			msg:   "it must fail passing an invalid operator",
			query: "size:>-10",
			err:   fmt.Errorf("invalid numeric operator: >-10"),
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
			filters := booleanToQueryList(q)
			for _, obj := range filters {
				got, _ := obj.(*query.NumericRangeQuery)
				if got == nil {
					t.Fatalf("expected to parse a *query.TermQuery, found=%T", obj)
				}
				want, ok := tt.want[got.FieldVal]
				if !ok {
					t.Fatalf("expected to find field=%v", got.FieldVal)
				}
				if want.FieldVal != got.FieldVal {
					t.Errorf("failed to match field values, want=%v, got=%v", want.FieldVal, got.FieldVal)
				}
				if toString(want.Min) != toString(got.Min) || toString(want.Max) != toString(got.Max) {
					t.Errorf("failed to match min/max, want=%s/%s, got=%s/%s",
						toString(want.Min), toString(want.Max), toString(got.Min), toString(got.Max))
				}
			}

		})
	}
}

func TestParseDateRangeQualifiers(t *testing.T) {
	toStr := func(v query.BleveQueryTime) string {
		if v.IsZero() {
			return "<zero>"
		}
		return v.Format(time.RFC3339)
	}
	for _, tt := range []struct {
		msg   string
		query string
		want  map[string]*query.DateRangeQuery
		err   error
	}{
		{
			msg:   "it must parse greater than date",
			query: "started:>2023-03-30",
			want: map[string]*query.DateRangeQuery{
				"started": {FieldVal: "started", Start: deterministicDate(2023, 3, 30, 00)},
			},
		},
		{
			msg:   "it must parse greater than datetime",
			query: "started:>2023-03-30T17:00:00Z",
			want: map[string]*query.DateRangeQuery{
				"started": {FieldVal: "started", Start: deterministicDate(2023, 3, 30, 17)},
			},
		},
		{
			msg:   "it must parse lesser than date",
			query: "completed:<2023-03-30",
			want: map[string]*query.DateRangeQuery{
				"completed": {FieldVal: "completed", End: deterministicDate(2023, 3, 30, 0)},
			},
		},
		{
			msg:   "it must parse lesser than datetime",
			query: "completed:<2023-03-30T15:00:00Z",
			want: map[string]*query.DateRangeQuery{
				"completed": {FieldVal: "completed", End: deterministicDate(2023, 3, 30, 15)},
			},
		},
		{
			msg:   "it must parse a range date",
			query: "started:2023-03-20..2023-03-30",
			want: map[string]*query.DateRangeQuery{
				"started": {
					FieldVal: "started",
					Start:    deterministicDate(2023, 3, 20, 00),
					End:      deterministicDate(2023, 3, 30, 00),
				}},
		},
		{
			msg:   "it must parse a range datetime",
			query: "started:2023-03-20T09:00:00Z..2023-03-20T21:00:00Z",
			want: map[string]*query.DateRangeQuery{
				"started": {
					FieldVal: "started",
					Start:    deterministicDate(2023, 3, 20, 9),
					End:      deterministicDate(2023, 3, 20, 21),
				}},
		},
		{
			msg:   "it must fail passing an invalid operator",
			query: "started:=2023-03-20",
			err:   fmt.Errorf("invalid date operator: =2023-03-20"),
		},
		{
			msg:   "it must fail passing an invalid date",
			query: "started:>2023-03-99",
			err:   fmt.Errorf(`parsing time "2023-03-99": day out of range`),
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
			filters := booleanToQueryList(q)
			for _, obj := range filters {
				got, _ := obj.(*query.DateRangeQuery)
				if got == nil {
					t.Fatalf("expected to parse a *query.TermQuery, found=%T", obj)
				}
				want, ok := tt.want[got.FieldVal]
				if !ok {
					t.Fatalf("expected to find field=%v", got.FieldVal)
				}
				if want.FieldVal != got.FieldVal {
					t.Errorf("failed to match field values, want=%v, got=%v", want.FieldVal, got.FieldVal)
				}
				if want.Start != got.Start || want.End != got.End {
					t.Errorf("failed to match start/end date, want=%s/%s, got=%s/%s",
						toStr(want.Start), toStr(want.End), toStr(got.Start), toStr(got.End))
				}
			}

		})
	}
}
