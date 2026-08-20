#!/usr/bin/env bash
# Populate a local cache of the alcatraz NER model, the files an agent needs on
# disk to serve DLP_PROVIDER=alcatraz masking rules that ask for PERSON,
# LOCATION or NRP.
#
# Published agent images bake the model in (hoophq/hoopagent's `-alcatraz`
# tags); a dev container mounts it from the host instead, and this is what fills
# that directory. Same origin and same manifest the image build reads, so the
# dev container runs the bytes CI ships — keep ALCATRAZ_MODEL_ORIGIN in sync
# with the `models` stage in Dockerfile.agent.
#
# Idempotent and safe to call on every `make run-dev`: it verifies the cache
# first and downloads only what is missing or corrupt. ~250MB on a cold cache,
# a few checksums on a warm one.
#
# Usage: alcatraz-models.sh [dest]     (default $HOME/.hoop/dev/alcatraz-models)

set -euo pipefail

ORIGIN="${ALCATRAZ_MODEL_ORIGIN:-https://d3pullut164aif.cloudfront.net/current}"
DEST="${1:-$HOME/.hoop/dev/alcatraz-models}"

# macOS ships shasum, Linux ships sha256sum, and neither ships the other.
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
else
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
fi

mkdir -p "$DEST"

# The manifest is fetched every run, never cached: it is what says whether the
# files on disk are still the ones the origin serves. A moving alias, so a stale
# cache from an older model is a mismatch here, not a silent skip.
manifest="${DEST}/.checksums.txt.new"
# Any exit before the mv below is a failed run; leave no half-written manifest
# behind for the next one to trip over.
trap 'rm -f "$manifest"' EXIT
curl -fsSL "${ORIGIN}/checksums.txt" -o "$manifest"

downloaded=0
verified=0
while read -r want path; do
  [ -n "$path" ] || continue
  file="${DEST}/${path}"
  if [ -f "$file" ] && [ "$(sha256 "$file")" = "$want" ]; then
    verified=$((verified + 1))
    continue
  fi
  echo "    fetching ${path}"
  mkdir -p "$(dirname "$file")"
  curl -fsSL "${ORIGIN}/${path}" -o "$file"
  got=$(sha256 "$file")
  if [ "$got" != "$want" ]; then
    # Not transient. The one benign cause is landing in the origin's 300s cache
    # window just after the model changed — rerun, never soften this.
    echo "    checksum mismatch for ${path}: expected ${want}, got ${got}" >&2
    rm -f "$file"
    exit 1
  fi
  downloaded=$((downloaded + 1))
done < "$manifest"

# The manifest lands last and under its published name, so an interrupted run
# leaves no file claiming the cache is complete. The agent reads it too:
# alcatraz checks every file against it on load.
mv "$manifest" "${DEST}/checksums.txt"

echo "    ${verified} cached, ${downloaded} downloaded -> ${DEST}"
