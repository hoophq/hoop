// Package http registers the HTTP codec with the inspect registry.
//
// The codec itself lives in github.com/hoophq/libhoop/v2/codec/http. This
// package is the seam between the two: libhoop may not import sidecar, so
// it cannot register itself, and something on this side has to do it.
//
// Import it for its side effect when a binary should speak HTTP:
//
//	import _ "github.com/hoophq/hoop/sidecar/codec/http"
//
// Options and New are re-exported so callers that configure the codec
// directly — the sidecar builds one per lane with its own capture settings —
// name one import path rather than reaching into libhoop themselves.
//
// Unlike the SQL codecs this one injects nothing. HTTP operations come from
// the request method, which the codec reads off the wire itself; there is no
// classifier to hand it.
package http

import (
	"github.com/hoophq/hoop/sidecar/inspect"
	codechttp "github.com/hoophq/libhoop/v2/codec/http"
)

// Options and Inspector are libhoop's types, not copies.
type (
	Options   = codechttp.Options
	Inspector = codechttp.Inspector
)

// New builds an HTTP codec.
//
// It returns the concrete type rather than inspect.Codec because the
// HTTP codec has a second entry point: InspectRequest, for a caller holding a
// parsed *http.Request instead of a socket. Narrowing to the interface here
// would hide it.
//
// It satisfies inspect.Codec anyway, structurally: Statement, Direction
// and Protocol are aliases of libhoop's types, so the method set lines up
// without libhoop ever naming this package.
func New(o Options) *Inspector { return codechttp.New(o) }

func init() {
	inspect.Register(func() inspect.Codec { return New(Options{}) })
}
