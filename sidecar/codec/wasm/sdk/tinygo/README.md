# hoop codec plug-ins in Go (TinyGo)

Package `codec` (module `github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo`)
is the Go SDK for the plug-in ABI in `../../abi/ABI.md`. A plug-in
implements `codec.Codec`, the capability interfaces it supports, and calls
`codec.Serve` from an `init` function. `example/acmewire` is a complete
plug-in and the template to copy.

## Install

```sh
go get github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo@v0.1.0
```

The module sits below the repository root, so its tag carries the module
path as a prefix: `sidecar/codec/wasm/sdk/tinygo/vX.Y.Z`. The repository's
own release tags (`1.162.0` and the like) do not version this module.

The `go` directive is 1.26, the Go minor the repository's CI runs.
TinyGo 0.43 supports Go 1.25 through 1.27, so TinyGo 0.43 with Go 1.26
or 1.27 builds a plug-in on this module.

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
`describe` probes one codec from the factory; a factory that hands a
later connection a type without the interface traps in that capability
export instead, with the connection and the capability named, and the
host reports the trap and drops the instance's connections.

| tag | capability | interface | exports |
|---|---|---|---|
| `hoop_deny` | `deny` | `Denier` | `deny` |
| `hoop_filter` | `filter` | `Filterer` | `filter` |
| `hoop_rewrite` | `rewrite` | `Rewriter` | `enable_rewrite`, `rewrite`, `flush` |
| `hoop_credential` | `credential` | `CredentialSource` | `take_credential` |
| `hoop_content` | `content` | `ContentRenderer` | `content` |

`-target wasm-unknown` is the freestanding target: no WASI, no scheduler,
and `main` never runs, so you call `Serve` from `init`. TinyGo exports
`_initialize`, which the host calls first and which runs those `init`
functions. `-opt=z -no-debug` shrinks the artifact.

## Not in go.work

This module is absent from the repository's `go.work`: the workspace pins
a Go version TinyGo does not follow, and `make test-sidecar` filters its
module walk against `go.work`. Under the workspace `go` refuses to work
here, so every command needs `GOWORK=off`:

```sh
GOWORK=off go test ./...
GOWORK=off go vet -tags hoop_deny,hoop_filter,hoop_rewrite,hoop_credential,hoop_content ./...
```

The package compiles and its tests run with the standard toolchain:
`host_tinygo.go` declares the `hoop` imports under the `tinygo` build tag
only, and off the relay the data-returning ones panic naming the import
while `Log` goes to stderr. This lets a plug-in unit-test its codec on the
host.

## No `.wasm` ships here

The SDK ships no artifact. Build `example/acmewire` with the command
above; it implements the same protocol as the Rust fixture under
`../../testdata`, so the same fixtures prove it:

```sh
hoop-inspect -codec-test acmewire.wasm ../../testdata/acmewire.fixtures.json
```
