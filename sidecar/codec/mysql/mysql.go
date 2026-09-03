// Package mysql registers the MySQL codec with the inspect registry.
//
// The codec itself lives in github.com/hoophq/libhoop/v2/codec/mysql. This
// package is the seam between the two: libhoop may not import sidecar, so it
// cannot register itself, and it cannot reach the SQL classifier either.
// Both of those happen here.
//
// Import it for its side effect when a binary should speak MySQL:
//
//	import _ "github.com/hoophq/hoop/sidecar/codec/mysql"
package mysql

import (
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	codecmysql "github.com/hoophq/libhoop/v2/codec/mysql"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// New builds a MySQL codec wired to sidecar's classifier and lexer.
//
// The splitter matters more here than it does for Postgres.
// CLIENT_MULTI_STATEMENTS is negotiated by Connector/J and most ORMs by
// default, so `SELECT 1; DROP TABLE users` arrives as ONE COM_QUERY. Without
// a splitter the codec classifies it by its leading verb and the DROP reaches
// the server having been evaluated as a select.
//
// A codec built without these decodes the wire but reports every statement as
// OpUnknown, which fails closed rather than waving traffic through. Nothing
// should construct the libhoop codec directly for production use; go through
// here so the classifier is always attached.
func New() inspect.Codec {
	return codecmysql.New(codecmysql.Options{
		Analyze: inspect.AnalyzeSQL,
		Split:   split,
	})
}

// split adapts lexer.Split to the injection point.
//
// It is not a direct assignment: lexer speaks Dialect, an internal uint8, and
// the codec speaks Protocol. Keeping the lexer's own vocabulary out of the
// injected signature is deliberate — libhoop would otherwise need to name a
// sidecar type to describe its own option.
//
// The dialect is not cosmetic. MySQL quotes identifiers with backticks, takes
// `#` as a line comment, and honours backslash escapes inside ordinary string
// literals; under Postgres rules `SELECT 'O\'Brien'; DELETE FROM t` splits
// inside the literal and the DELETE disappears.
//
// The protocol argument is ignored rather than switched on. This package
// registers exactly one codec and hands this function to it alone, so the
// only value it can receive is MySQL; a fallback branch would be dead code
// whose wrong answer is a silent misread rather than a build failure. The
// parameter stays because codectypes.Splitter declares it.
func split(sql string, _ codectypes.Protocol) []string {
	return lexer.Split(sql, lexer.MySQL)
}

func init() {
	inspect.Register(func() inspect.Codec { return New() })
}
