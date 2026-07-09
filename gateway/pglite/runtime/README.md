# PGlite WASI runtime artifact

`pglite-runtime.tar.gz` contains the PGlite WASI build: PostgreSQL 17.5
compiled to a `wasm32-wasi` (preview 1) module plus the share files it needs
at runtime (`postgres.bki`, system SQL, timezone data, the `plpgsql`
extension and the default `password` file).

It is embedded into the gateway binary via `go:embed` and extracted into the
configured data directory on first boot (see `pglite.go`).

## Provenance

The archive content is byte-identical to the upstream artifact; only the
compression was changed from xz to gzip so extraction needs nothing outside
the Go standard library.

| | |
|---|---|
| Source artifact | `assets/pglite-wasi.tar.xz` from <https://github.com/kshcherban/pglite-rust-bindings> @ `master` (2026-06-10) |
| Source sha256 | `e3d1cfd67505a6056be00899cfcf0e176420d521a2a53d9016bf63814486b47e` |
| This file sha256 | `6bcde7b16d47964a74b277b6a5afb9a7339dab299826d16c93c834b842548997` |
| Upstream build | built from <https://github.com/electric-sql/pglite-bindings> (17.x), PostgreSQL 17.5, wasi-sdk clang 19.1.5 |

## Repackaging

```sh
xz -dc pglite-wasi.tar.xz | gzip -9 > pglite-runtime.tar.gz
```

## Updating

To move to a newer PGlite build, regenerate the archive from the
`electric-sql/pglite-bindings` build tooling (or a newer upstream artifact),
update the table above, and run the `gateway/pglite` test suite — it boots
the runtime and applies the full hoop migration set against it.
