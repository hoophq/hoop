// The x-memcache plug-in. Not in go.work, like the SDK it replaces below:
// the workspace pins a Go version TinyGo does not follow, and `make
// test-sidecar` filters its module walk against go.work, so this module
// never enters the standard-toolchain loop. `make test-sidecar-plugins`
// tests it under GOWORK=off; README.md has the commands.
module github.com/hoophq/hoop/sidecar/codec/wasm/examples/memcache

go 1.26

require github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo v0.0.0

replace github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo => ../../sdk/tinygo
