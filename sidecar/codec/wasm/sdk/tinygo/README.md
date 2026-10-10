# hoop codec plug-ins in Go (TinyGo)

Package `codec` (module `github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo`)
is the Go SDK for the plug-in ABI in `../../abi/ABI.md`. A plug-in
implements `codec.Codec`, the capability interfaces it supports, and calls
`codec.Serve` from an `init` function. `example/acmewire` is a complete
plug-in and the template to copy.

## Build

```sh
tinygo build -o acmewire.wasm -target wasm-unknown \
  -tags hoop_deny,hoop_filter,hoop_rewrite,hoop_credential,hoop_content \
  ./example/acmewire
```

Each optional export lives in a file behind a build tag, so the exports of
the module and the `capabilities` of the manifest cannot disagree: the
host refuses a capability without its export and an export without its
capability, and `describe` traps when the manifest names a capability the
tags did not turn on, or one the codec's type does not implement.

| tag | capability | interface | exports |
|---|---|---|---|
| `hoop_deny` | `deny` | `Denier` | `deny` |
| `hoop_filter` | `filter` | `Filterer` | `filter` |
| `hoop_rewrite` | `rewrite` | `Rewriter` | `enable_rewrite`, `rewrite`, `flush` |
| `hoop_credential` | `credential` | `CredentialSource` | `take_credential` |
| `hoop_content` | `content` | `ContentRenderer` | `content` |

`-target wasm-unknown` is the freestanding target: no WASI, no scheduler,
and `main` never runs, which is why `Serve` is called from `init`. TinyGo
exports `_initialize`, which the host calls first and which runs those
`init` functions. `-opt=z -no-debug` shrinks the artifact.

## Not in go.work

This module is deliberately absent from the repository's `go.work`: the
workspace pins a Go version TinyGo does not follow, and `make test-sidecar`
filters its module walk against `go.work` on purpose. Under the workspace
`go` refuses to work here, so every command needs `GOWORK=off`:

```sh
GOWORK=off go test ./...
GOWORK=off go vet -tags hoop_deny,hoop_filter,hoop_rewrite,hoop_credential,hoop_content ./...
```

The package compiles and its tests run with the standard toolchain: the
`hoop` imports are declared only under the `tinygo` build tag
(`host_tinygo.go`), and off the relay the data-returning ones panic
naming the import while `Log` goes to stderr. That is what lets a plug-in
unit-test its codec on the host.

## TinyGo is not installed on the dev box

`command -v tinygo` finds nothing here, so no `.wasm` is checked in for
this SDK and the wasm build above is unverified on this machine. The
checked-in conformance fixture is the Rust example
(`../../testdata/acmewire.wasm`); `example/acmewire` implements the same
protocol and the same fixtures apply to it once built. Install TinyGo
(`brew install tinygo`), build, then
`hoop-inspect -codec-test acmewire.wasm ../../testdata/acmewire.fixtures.json`.
