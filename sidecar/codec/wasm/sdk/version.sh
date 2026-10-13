#!/usr/bin/env sh
# Prints the SDK version: the one number the crate, the Go module and the
# C header are released under. VERSION is the source; Cargo.toml repeats
# it because cargo reads only its own manifest, and Cargo.lock records it
# because `cargo publish --locked` refuses a lockfile that disagrees with
# the manifest, which would fail the release after the tag exists. The
# three must agree, and every workflow that reads the version goes
# through here so the check runs before anything is tagged or published.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
version=$(tr -d '[:space:]' < "$here/VERSION")
cargo_version=$(sed -nE 's/^version = "([^"]+)"$/\1/p' "$here/rust/Cargo.toml" | head -1)
# The lockfile entry for the crate itself: the version line that follows
# its name line. The sed holds the name and prints the next version.
lock_version=$(sed -nE '/^name = "hoop-codec"$/{n;s/^version = "([^"]+)"$/\1/p;}' "$here/rust/Cargo.lock" | head -1)

if ! echo "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "sidecar/codec/wasm/sdk/VERSION holds '$version', not X.Y.Z" >&2
  exit 1
fi
if [ "$version" != "$cargo_version" ]; then
  echo "sidecar/codec/wasm/sdk/VERSION says $version but sidecar/codec/wasm/sdk/rust/Cargo.toml says $cargo_version; bump both in the same PR" >&2
  exit 1
fi
if [ "$version" != "$lock_version" ]; then
  echo "sidecar/codec/wasm/sdk/VERSION says $version but sidecar/codec/wasm/sdk/rust/Cargo.lock records hoop-codec $lock_version; run cargo update -w in sdk/rust and commit the lockfile" >&2
  exit 1
fi
echo "$version"
