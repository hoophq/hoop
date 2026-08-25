// sidecar is a NESTED module on purpose.
//
// It is the wiring layer: the one place that picks concrete codecs and hands
// them to the Gate. Those codecs ship in github.com/hoophq/libhoop/v2,
// a private module, so the dependency lives here rather than in the root
// github.com/hoophq/hoop/hoopinspect module, which stays at zero
// dependencies and stays buildable without credentials.
//
// Keeping the direction one-way — sidecar imports libhoop, never the reverse
// — is what stops hoopinspect and libhoop from becoming a release cycle that
// can only move in lockstep.
module github.com/hoophq/hoop/hoopinspect/sidecar

go 1.26.5

toolchain go1.26.5

require (
	github.com/hoophq/hoop/hoopinspect v0.0.0
	github.com/hoophq/libhoop/v2 v2.0.0
)

replace github.com/hoophq/hoop/hoopinspect => ..
