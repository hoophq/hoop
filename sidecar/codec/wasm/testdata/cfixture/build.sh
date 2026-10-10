#!/bin/sh
# Builds every x-cfix variant into ../ from cfixture.c. The repo carries
# the binaries so the tests need no C toolchain; run this after editing
# the source.
#
# Needs clang with the wasm32 target and wasm-ld on PATH: `brew install
# llvm lld` on macOS (CC=/opt/homebrew/opt/llvm/bin/clang), or any LLVM
# >= 15 elsewhere.
set -eu
cd "$(dirname "$0")"
CC=${CC:-clang}

build() {
  out=$1
  shift
  "$CC" --target=wasm32-unknown-unknown -nostdlib -O2 -fuse-ld=lld \
    -Wl,--no-entry -Wl,--strip-all -o "../$out" "$@" cfixture.c
  echo "built $out ($(wc -c <"../$out" | tr -d ' ') bytes)"
}

# The well-formed module and its misbehaving twins.
build cfixture.wasm
build cfixture_trap.wasm -DDECODE_TRAP
build cfixture_spin.wasm -DDECODE_SPIN -DEXTRA='",\"call_timeout_ms\":200"'
build cfixture_grow.wasm -DGROW -DEXTRA='",\"memory_limit_pages\":4"'
build cfixture_lane.wasm -DEXTRA='",\"instances\":\"per_lane\""'
build cfixture_sql.wasm -DSQL -DEXTRA='",\"sql_dialect\":\"postgres\""'
build cfixture_wasi.wasm -DWASI -DEXTRA='",\"wasi\":true"'
build cfixture_maskabuse.wasm -DMASKABUSE

# Each of these breaks one load-time rule.
build cfixture_badabi.wasm -DABI_VERSION='"2"'
build cfixture_builtin.wasm -DPROTOCOL='"postgres"'
build cfixture_noprefix.wasm -DPROTOCOL='"cfix"'
build cfixture_capnoexport.wasm -DCAPS='"[\"deny\",\"filter\"]"'
build cfixture_exportnocap.wasm -DCAPS='"[]"'
build cfixture_undeclared.wasm -DUNDECLARED
build cfixture_nodialect.wasm -DSQL
build cfixture_wasiundeclared.wasm -DWASI
