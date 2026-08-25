package sidecar

import (
	"github.com/hoophq/hoop/hoopinspect"
	codechttp "github.com/hoophq/libhoop/v2/codec/http"
)

// newHTTPCodec returns a factory producing HTTP codecs with the lane's
// capture settings.
//
// It is the one place the sidecar names codec/http directly. Everywhere else
// the sidecar reaches codecs through the registry, which is what lets a
// binary link only the protocols it speaks; this file is already inside a
// package that imports codec/all, so it adds no reach.
//
// The factory returns a FRESH codec per call. Two connections sharing one
// stateful codec corrupt each other's reassembly buffer.
func newHTTPCodec(cfg HTTPCodecConfig) func() hoopinspect.Codec {
	opts := codechttp.Options{
		CaptureBody:  cfg.CaptureBody,
		MaxBodyBytes: cfg.MaxBodyBytes,
		Headers:      cfg.Headers,
	}
	return func() hoopinspect.Codec { return codechttp.New(opts) }
}
