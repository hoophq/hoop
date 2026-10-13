# hoop codec plug-ins in C

`hoop_codec.h` is the whole SDK: one C99 header with the import
declarations of module `hoop`, the `HOOP_EXPORT` attribute, the packed
`u64` helpers every export returns through, and, under
`HOOP_CODEC_ARENA`, an `alloc`/`free` pair that needs no libc. The
skeleton at the end of the file is a complete plug-in to copy. The ABI
it speaks is `../../abi/ABI.md`.

## Install

The header is a release asset, not a package. Each release of the SDK
attaches it to the `hoop-codec-vX.Y.Z` tag on GitHub:

```sh
curl -fsSLO https://github.com/hoophq/hoop/releases/download/hoop-codec-vX.Y.Z/hoop_codec.h
```

Replace `X.Y.Z` with an SDK version from the Releases page, and pin the
one your plug-in was proven against. A copy of the file in your source
tree is the whole dependency; there is nothing to link.

## Build

```sh
clang --target=wasm32-unknown-unknown -nostdlib -O2 -fno-builtin \
  -Wl,--no-entry -o plugin.wasm plugin.c
```

Linking needs `wasm-ld` (LLVM's lld). `HOOP_EXPORT` sets `export_name`,
so no `--export` flag is needed, and a module calls only the imports it
needs: the linker drops the rest, and a module that never calls
`hoop_analyze_sql` owes the host no `sql_dialect`.

Prove the module against the ABI, then against your fixtures:

```sh
hoop-inspect -codec-test plugin.wasm plugin.fixtures.json
```
