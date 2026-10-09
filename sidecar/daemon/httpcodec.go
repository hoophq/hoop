package daemon

import (
	"github.com/hoophq/hoop/sidecar/analyzer"
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
// A nil cfg captures no header but the approval mode, and no body unless the
// lane holds. The lane still gets its own factory, because the Via loop
// marker and the Connect Gateway resource normalization apply to every http
// lane, capture or not.
//
// Every http lane captures analyzer.ClientModeHeaders, so a client can opt
// into return per request (ADR-0021). The codec records only headers the
// client sent, so other requests keep their audit shape.
//
// A lane that holds captures request bodies even with capture_body off: a
// review filed without the body would release the next request to that
// target whatever it carries. The body then reaches policy, the analyzer and
// the audit trail too, as capture_body would send it. The factory swaps with
// the rules (see relayRules), so holds always matches the policy the
// connection runs.
//
// The factory returns a FRESH codec per call. Two connections sharing one
// stateful codec corrupt each other's reassembly buffer, and would trade
// each other's credentials.
func newHTTPCodec(cfg *HTTPCodecConfig, credentialHeader string, holds bool) func() inspect.Codec {
	opts := codechttp.Options{
		// headerNames is normalized once, the same list validate checked,
		// nil-safe, and a fresh slice, so the append leaves cfg alone.
		Headers:            append(cfg.headerNames(), analyzer.ClientModeHeaders()...),
		CredentialHeader:   credentialHeader,
		CaptureRequestBody: holds,
	}
	if cfg != nil {
		opts.CaptureBody = cfg.CaptureBody
		opts.MaxBodyBytes = cfg.MaxBodyBytes
		opts.SensitiveQueryParams = cfg.SensitiveQueryParams
	}
	return func() inspect.Codec { return codechttp.New(opts) }
}
