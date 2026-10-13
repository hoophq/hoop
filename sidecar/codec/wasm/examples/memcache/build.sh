#!/usr/bin/env bash
# Builds dist/memcache.wasm with TinyGo and copies it to memcache.wasm
# beside fixtures.json. The repository carries that copy: `make
# test-sidecar-plugins` proves it against the fixtures without TinyGo, and
# the plug-in workflow rebuilds the module from source and proves that
# build against the same fixtures. The SDK is the module two directories
# up, through the replace in go.mod.
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
# this module are outside go.work on purpose; go.mod says why. The build
# runs from this directory: TinyGo resolves the package through `go list`
# relative to the working directory, and an absolute path from a directory
# with no go.mod (the repository root under GOWORK=off) fails with "cannot
# find main module".
cd "$here"
GOWORK=off tinygo build \
  -o dist/memcache.wasm \
  -target wasm-unknown -opt=z -no-debug \
  -tags hoop_deny,hoop_content \
  .
cp dist/memcache.wasm memcache.wasm
ls -l memcache.wasm
