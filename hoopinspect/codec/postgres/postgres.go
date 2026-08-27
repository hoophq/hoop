// Package postgres registers the Postgres codec with hoopinspect.
//
// The codec itself lives in github.com/hoophq/libhoop/v2/codec/postgres. This
// package is the seam between the two: libhoop may not import hoopinspect, so
// it cannot register itself, and it cannot reach the SQL classifier either.
// Both of those happen here.
//
// Import it for its side effect when a binary should speak Postgres:
//
//	import _ "github.com/hoophq/hoop/hoopinspect/codec/postgres"
//
// Importing this one alone rather than codec/all is what keeps the MSSQL and
// HTTP machinery out of a binary that only fronts a Postgres upstream.
package postgres

import (
	"github.com/hoophq/hoop/hoopinspect"
	"github.com/hoophq/hoop/hoopinspect/lexer"
	codecpg "github.com/hoophq/libhoop/v2/codec/postgres"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// New builds a Postgres codec wired to hoopinspect's classifier and lexer.
//
// A codec built without them decodes the wire but reports every statement as
// OpUnknown, which fails closed rather than waving traffic through. Nothing
// should construct the libhoop codec directly for production use; go through
// here so the classifier is always attached.
func New() hoopinspect.Codec {
	return codecpg.New(codecpg.Options{
		Analyze: hoopinspect.AnalyzeSQL,
		Split:   split,
	})
}

// split adapts lexer.Split to the injection point.
//
// It is not a direct assignment: lexer speaks Dialect, an internal uint8, and
// the codec speaks Protocol. Keeping the lexer's own vocabulary out of the
// injected signature is deliberate — libhoop would otherwise need to name a
// hoopinspect type to describe its own option.
//
// The dialect is not cosmetic: '[' opens a quoted identifier in T-SQL and is
// an array subscript in PostgreSQL, so one set of lexical rules cannot serve
// both without mangling one of them.
func split(sql string, proto codectypes.Protocol) []string {
	d := lexer.Postgres
	if proto == codectypes.MSSQL {
		d = lexer.MSSQL
	}
	return lexer.Split(sql, d)
}

func init() {
	hoopinspect.Register(func() hoopinspect.Codec { return New() })
}
