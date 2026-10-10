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

go 1.26.8

require (
	github.com/hoophq/hoop/sidecar v0.0.0
	github.com/tetratelabs/wazero v1.12.0
)

require (
	github.com/hoophq/libhoop v0.0.0-20261006134038-864e6e333f40 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/hoophq/hoop/sidecar => ../..

// TEMPORARY until https://github.com/wazero/wazero/pull/2507 ships in a
// release: the fork allows fd_renumber onto stdio fds, which the embedded
// PGlite database (gateway/pglite) needs. This module does not need the fix,
// but the pin MUST match gateway/go.mod and client/go.mod: go.work resolves
// one wazero for the whole workspace, and two modules disagreeing on the
// replacement make `hoop start sidecar` (which links both) unbuildable.
// Drop the replace here when the gateway drops its own.
replace github.com/tetratelabs/wazero => github.com/racerxdl/wazero v1.12.1-0.20260610204201-d1e18e798de6
