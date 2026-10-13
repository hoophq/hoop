#!/usr/bin/env bash
# Builds dist/memcache.wasm with TinyGo and copies it to memcache.wasm
# beside fixtures.json. The repository carries that copy: `make
# test-sidecar-plugins` proves it against the fixtures without TinyGo, and
# the plug-in workflow rebuilds it and fails on a digest that differs. The
# SDK is the module two directories up, through the replace in go.mod.
#
# Needs TinyGo 0.33 or later:
#   brew tap tinygo-org/tools && brew install tinygo
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"

if ! command -v tinygo >/dev/null 2>&1; then
  echo "build.sh: tinygo is not installed; the module is built with TinyGo (https://tinygo.org):" >&2
  echo "  brew tap tinygo-org/tools && brew install tinygo" >&2
  exit 1
fi

mkdir -p "$here/dist"

# -tags must name the same capability set as the manifest in main.go:
# describe traps at load when they differ. GOWORK=off because the SDK and
# this module are outside go.work on purpose; go.mod says why.
GOWORK=off tinygo build \
  -o "$here/dist/memcache.wasm" \
  -target wasm-unknown -opt=z -no-debug \
  -tags hoop_deny,hoop_content \
  "$here"
cp "$here/dist/memcache.wasm" "$here/memcache.wasm"
ls -l "$here/memcache.wasm"
