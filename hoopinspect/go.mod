// hoopinspect deliberately has ZERO dependencies.
//
// That is the product requirement, not an accident: the library exists to be
// audited and embedded by people who will not adopt a proxy. A dependency-free
// stdlib module can be read end-to-end in an afternoon, vendored without a
// supply-chain review, and compiled to `GOOS=wasip1 GOARCH=wasm` for an Envoy
// network filter. Adding a dependency here is a breaking change to the pitch.
//
// Test dependencies are held to the same bar: the tests use only `testing`.
module github.com/hoophq/hoop/hoopinspect

go 1.26.5
