#!/usr/bin/env bash
# Builds the x-resp plug-in for wasm32-unknown-unknown into dist/resp.wasm
# and copies it to resp.wasm beside fixtures.json. The repository carries
# that copy: `make test-sidecar-plugins` proves it against the fixtures
# without a Rust wasm target, and the plug-in workflow rebuilds the module
# from source and proves that build against the same fixtures.
#
# Needs: rustup target add wasm32-unknown-unknown
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"

cargo build --release --target wasm32-unknown-unknown \
  --manifest-path "$here/Cargo.toml" --target-dir "$here/target"
mkdir -p "$here/dist"
cp "$here/target/wasm32-unknown-unknown/release/resp.wasm" "$here/dist/resp.wasm"
cp "$here/dist/resp.wasm" "$here/resp.wasm"
ls -l "$here/resp.wasm"
