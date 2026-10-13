#!/usr/bin/env bash
# Compiles arena_test.c against hoop_codec.h with the native C compiler
# and runs it. The arena is plain C99 with nothing wasm in its logic, so
# the lifetime rule its callers rely on is proven without a wasm target;
# the wasm-only attributes on the import and export declarations are
# ignored by a native compiler with a warning. `make test-sidecar-plugins`
# runs this.
#
# Environment:
#   CC   the C compiler; default cc
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
CC="${CC:-cc}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

"$CC" -std=c99 -O1 -Wall -Wextra -Wno-unknown-attributes -Wno-attributes -Wno-ignored-attributes \
  -I"$here" -o "$tmp/arena_test" "$here/arena_test.c"
"$tmp/arena_test"
