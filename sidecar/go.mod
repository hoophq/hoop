// The sidecar module has exactly ONE dependency: github.com/hoophq/libhoop.
//
// It used to have none, and that was a product requirement rather than an
// accident — a dependency-free stdlib module can be read end to end in an
// afternoon, vendored without a supply-chain review, and compiled to
// `GOOS=wasip1 GOARCH=wasm` for an Envoy network filter.
//
// That ended when the protocol codecs moved to libhoop. The codecs are the
// thing that turns bytes into the Statement a policy evaluates, so this
// module cannot describe its own inputs without naming libhoop's types. They
// are aliased in wiretypes.go rather than copied: one definition of the
// policy document, no conversion on the hot path, no drift.
//
// libhoop is PRIVATE. Building or testing this module therefore needs
// GOPRIVATE=github.com/hoophq/libhoop and credentials for that repository.
// Anyone without them cannot build this module at all — that is the cost of
// the codecs being private, and it is deliberate.
//
// The dependency runs one way. libhoop imports nothing from this repository.
module github.com/hoophq/hoop/sidecar

go 1.26.5

require github.com/hoophq/libhoop v0.0.0-20260827130908-d96eb386cba0
