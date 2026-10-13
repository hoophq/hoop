#!/usr/bin/env bash
# Builds dist/sqlframe.wasm from sqlframe.cpp against the C header two
# directories up (../../sdk/c) and copies it to sqlframe.wasm beside
# fixtures.json. The repository carries that copy: `make
# test-sidecar-plugins` proves it against the fixtures without clang or
# lld, and the plug-in workflow rebuilds it and fails on a digest that
# differs.
#
# Needs clang with the wasm32 target and wasm-ld (LLVM's lld). On macOS:
# `brew install llvm lld`, then CC=/opt/homebrew/opt/llvm/bin/clang++,
# because Apple clang ships no wasm target. Any LLVM >= 15 works elsewhere.
#
# Environment:
#   CC            the clang++ to use; default clang++
#   WASM_LD_DIR   a directory holding wasm-ld, prepended to PATH when set
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
include="$here/../../sdk/c"
CC="${CC:-clang++}"

if [ -n "${WASM_LD_DIR:-}" ]; then
  PATH="$WASM_LD_DIR:$PATH"
fi
if ! command -v wasm-ld >/dev/null 2>&1 && [ ! -x "$(dirname "$(command -v "$CC")")/wasm-ld" ]; then
  echo "build.sh: wasm-ld not found; brew install lld (or set WASM_LD_DIR to a directory holding it)" >&2
  exit 1
fi

mkdir -p "$here/dist"
"$CC" --target=wasm32-unknown-unknown -std=c++17 \
  -nostdlib -fno-builtin -fno-exceptions -fno-rtti -O2 \
  -Wall -Wextra -I"$include" \
  -fuse-ld=lld -Wl,--no-entry -Wl,--strip-all \
  -o "$here/dist/sqlframe.wasm" "$here/sqlframe.cpp"
cp "$here/dist/sqlframe.wasm" "$here/sqlframe.wasm"
ls -l "$here/sqlframe.wasm"
