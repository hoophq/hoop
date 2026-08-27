// Package mssql registers the MSSQL codec with the inspect registry.
//
// The codec itself lives in github.com/hoophq/libhoop/v2/codec/mssql. This
// package is the seam between the two: libhoop may not import sidecar, so
// it cannot register itself, and it cannot reach the SQL classifier either.
//
// Import it for its side effect when a binary should speak MSSQL:
//
//	import _ "github.com/hoophq/hoop/sidecar/codec/mssql"
package mssql

import (
	"github.com/hoophq/hoop/sidecar/inspect"
	codecmssql "github.com/hoophq/libhoop/v2/codec/mssql"
)

// New builds an MSSQL codec wired to sidecar's classifier.
//
// No splitter, unlike Postgres: this codec hands a SQLBatch to the analyzer
// whole rather than splitting it, so there is nothing to inject.
//
// A codec built without the classifier decodes TDS but reports every
// statement as OpUnknown, which fails closed. Go through here rather than
// constructing the libhoop codec directly, so the classifier is attached.
func New() inspect.Codec {
	return codecmssql.New(codecmssql.Options{Analyze: inspect.AnalyzeSQL})
}

func init() {
	inspect.Register(func() inspect.Codec { return New() })
}
