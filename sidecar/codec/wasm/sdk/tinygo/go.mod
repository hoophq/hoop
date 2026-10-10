// The Go SDK for hoop codec plug-ins, built with TinyGo for
// wasm32-unknown-unknown. Not in go.work: the workspace pins a Go version
// TinyGo does not follow, and `make test-sidecar` filters its module walk
// against go.work. Build and test it with GOWORK=off; README.md has the
// commands.
module github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo

go 1.22
