// The WASM codec plug-in host is a nested module because it needs wazero,
// the same reason descriptors/gcs needs oauth2: the root carries libhoop and
// nothing else.
//
// wazero is the only pure-Go WebAssembly runtime with no cgo and no
// dependencies of its own, which is what lets the relay stay one static
// binary while loading a codec it was not built with. A plug-in is one
// module compiled from any wasm32 toolchain; abi/ABI.md is the contract.
//
// A binary that does not import this module loads no plug-in and refuses a
// config that names one.
module github.com/hoophq/hoop/sidecar/codec/wasm

go 1.26.10

require (
	github.com/hoophq/hoop/sidecar v0.0.0
	github.com/tetratelabs/wazero v1.12.0
)

require (
	github.com/hoophq/libhoop v0.0.0-20261027205623-14b215ebae10 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/hoophq/hoop/sidecar => ../..

// This module runs upstream wazero. gateway/go.mod and client/go.mod replace
// it with a fork for PGlite's fd_renumber (wazero/wazero#2507); under go.work
// that replace reaches this module too, and the fork differs from upstream
// in that one WASI call, which no codec plug-in makes. A build outside the
// workspace (GOWORK=off) runs upstream and the tests pass on both.
