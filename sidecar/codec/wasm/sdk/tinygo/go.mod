// The Go SDK for hoop codec plug-ins, built with TinyGo for
// wasm32-unknown-unknown. Not in go.work: the workspace pins a Go version
// TinyGo does not follow, and `make test-sidecar` filters its module walk
// against go.work. Build and test it with GOWORK=off; README.md has the
// commands.
//
// Released as a nested module: the tag is
// sidecar/codec/wasm/sdk/tinygo/vX.Y.Z, which is the only form the module
// proxy accepts for a go.mod below the repository root.
//
// The go directive is the workspace's Go minor: CI runs that toolchain
// with GOTOOLCHAIN=local, so a newer directive fails `make
// test-sidecar-plugins`. TinyGo 0.43 compiles Go 1.25 through 1.27.
module github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo

go 1.26
