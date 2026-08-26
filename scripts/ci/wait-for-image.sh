#!/usr/bin/env bash
# Wait until image references are visible in the registry.
#
# Docker Hub is eventually consistent right after a push, manifest lists most of
# all: the job that pushed a tag can succeed seconds before another runner can
# resolve it. Jobs that build FROM a tag one of their `needs` just pushed race
# that window, so they call this first and fail with a clear message instead of
# a `manifest unknown` in the middle of a build.
#
# Usage: wait-for-image.sh <image-ref> [<image-ref> ...]

set -euo pipefail

ATTEMPTS=5
DELAY=5

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <image-ref> [<image-ref> ...]" >&2
  exit 2
fi

for ref in "$@"; do
  ok=""
  for i in $(seq 1 "$ATTEMPTS"); do
    if docker manifest inspect "$ref" >/dev/null 2>&1; then
      ok=1
      break
    fi
    echo "waiting for ${ref} to be visible (${i}/${ATTEMPTS})"
    sleep "$DELAY"
  done
  if [ -z "$ok" ]; then
    echo "::error::${ref} not visible after ${ATTEMPTS} attempts"
    exit 1
  fi
  echo "${ref} is visible"
done
