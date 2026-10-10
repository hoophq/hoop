#!/usr/bin/env bash
# Builds the x-acmewire plug-in and copies it to the host's testdata, where
# the conformance tests replay acmewire.fixtures.json against it. The
# repository carries the artifact: the Go tests must not need a Rust toolchain.
#
# Needs: rustup target add wasm32-unknown-unknown
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
out="$here/../../../../testdata/acmewire.wasm"

cargo build --release --target wasm32-unknown-unknown --manifest-path "$here/Cargo.toml"
cp "$here/../../target/wasm32-unknown-unknown/release/acmewire.wasm" "$out"
ls -l "$out"
