# Codec plug-in examples

Three plug-ins against the ABI in `../abi/ABI.md`, one per guest toolchain.
The reference example, `x-acmewire`, stays with the Rust SDK at
`../sdk/rust/examples/acmewire`; its artifact is the host's conformance
fixture under `../testdata/`.

| directory | protocol | language | shows |
|---|---|---|---|
| `resp/` | `x-resp`, Redis RESP2 | Rust, `../sdk/rust` | `deny`, `rewrite` masking of bulk strings at any depth, `credential` for AUTH |
| `memcache/` | `x-memcache`, the memcached text protocol | Go, `../sdk/tinygo` | a line protocol with data blocks, `deny`, `content` for the AI analyzer |
| `sqlframe/` | `x-sqlframe`, SQL in a binary frame | freestanding C++, `../sdk/c` | the host classifier through `analyze_sql` and `split_sql`, a `filter` that strips padding |

Each directory holds the source, `build.sh`, `fixtures.json`, a
`config.yaml` for a local lane, a README with the protocol and the rules
it can fence, and the built module `<name>.wasm`.

## Build

Every `build.sh` writes `dist/<name>.wasm` and copies it to `<name>.wasm`
beside `fixtures.json`. The SDK paths are relative (`../../sdk/<lang>`), so
the build runs from a checkout with no environment.

| example | needs |
|---|---|
| `resp` | Rust 1.82+, `rustup target add wasm32-unknown-unknown` |
| `memcache` | TinyGo 0.33+ with `wasm-opt`; `GOWORK=off` for every `go` command, because the module is not in `go.work` |
| `sqlframe` | clang with the wasm32 target and `wasm-ld` (LLVM 15+); `CC` and `WASM_LD_DIR` select them |

## The artifact is checked in

`<name>.wasm` is committed. `make test-sidecar-plugins` at the repository
root, which `make test-oss` depends on, runs the host-side tests of the
SDKs and examples and proves every committed module against the ABI and
its fixtures with `hoop-inspect -codec-test`. That job has Go and Rust and
nothing else, so it never builds a module.

The path-filtered plug-in workflow installs the three guest toolchains,
rebuilds every example from source and proves the rebuilt module against
the same fixtures. It does not compare bytes: rustc on Linux emits a
module that differs from the macOS build of the same source. A source
change under an example or an SDK still ships with the artifact its
`build.sh` produces, because the committed copy is what `make
test-sidecar-plugins` proves.

## Release

The three SDKs share one version, `../sdk/VERSION`. A PR bumps it and
`../sdk/rust/Cargo.toml` together; the plug-in workflow refuses the PR
when they differ. The merge to main tags `hoop-codec-vX.Y.Z`, and from
that tag the release workflow publishes the crate, tags the Go module
`sidecar/codec/wasm/sdk/tinygo/vX.Y.Z` at the same commit, attaches
`hoop_codec.h` to the GitHub release, and opens a PR against
hoophq/plugins with copies of these examples pinned to `vX.Y.Z` in place
of the relative paths. No tag is pushed by hand.
