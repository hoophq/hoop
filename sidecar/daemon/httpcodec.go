package daemon

import (
	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// newHTTPCodec returns a factory producing HTTP codecs with the lane's
// capture settings and the header its credential is lifted from.
//
// It is the one place the sidecar names codec/http directly. Everywhere else
// the sidecar reaches codecs through the registry, which is what lets a
// binary link only the protocols it speaks; this file is already inside a
// package that imports codec/all, so it adds no reach.
//
// A nil cfg captures nothing, as the registry default does. The lane still
// gets its own factory, because the Via loop marker and the Connect Gateway
// resource normalization apply to every http lane, capture or not.
//
// The factory returns a FRESH codec per call. Two connections sharing one
// stateful codec corrupt each other's reassembly buffer, and would trade
// each other's credentials.
func newHTTPCodec(cfg *HTTPCodecConfig, credentialHeader string) func() inspect.Codec {
	opts := codechttp.Options{
		Headers:          cfg.headerNames(), // normalized once, the same list validate checked; nil-safe
		CredentialHeader: credentialHeader,
	}
	if cfg != nil {
		opts.CaptureBody = cfg.CaptureBody
		opts.MaxBodyBytes = cfg.MaxBodyBytes
		opts.SensitiveQueryParams = cfg.SensitiveQueryParams
	}
	return func() inspect.Codec { return codechttp.New(opts) }
}
