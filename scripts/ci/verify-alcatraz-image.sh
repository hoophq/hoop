#!/usr/bin/env bash
# Verify that a published alcatraz agent image still carries an intact NER
# model, once per architecture in its manifest list.
#
# The build already checksummed the weights in the download stage; this checks
# the artifact that actually landed in the registry, which a single-platform
# build stage cannot. The manifest ships inside the image, so the answer comes
# from the image itself and nothing here needs to know a model id or digest.
#
# The files are read by exporting the container filesystem rather than by
# running sha256sum inside the container: hoophq/hoopagent:<tag>-distroless has
# no shell and no coreutils, and one verification path for every flavour beats
# two that can drift.
#
# Usage: verify-alcatraz-image.sh <image-ref> [<image-ref> ...]

set -euo pipefail

MODELS_DIR=opt/alcatraz/models
PLATFORMS=(linux/amd64 linux/arm64)

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <image-ref> [<image-ref> ...]" >&2
  exit 2
fi

# Every failure below is expected in normal CI use (a missing or corrupt model
# is what this script hunts for), and errexit turns each one into an immediate
# exit, so the container and the export directory are released from a trap
# rather than from the happy path.
cid=""
workdir=""

cleanup() {
  if [ -n "$cid" ]; then
    docker rm -f "$cid" >/dev/null 2>&1 || true
    cid=""
  fi
  if [ -n "$workdir" ]; then
    rm -rf "$workdir"
    workdir=""
  fi
}
trap cleanup EXIT

for image in "$@"; do
  for platform in "${PLATFORMS[@]}"; do
    echo "verifying ${image} (${platform})"
    workdir=$(mktemp -d)
    # create, not run: nothing in the image is executed, so this works the same
    # for an emulated architecture with no binfmt handler on the runner. The
    # trailing argument is what a bare `docker create` demands from an image
    # carrying neither ENTRYPOINT nor CMD; it is never executed.
    cid=$(docker create --platform "$platform" "$image" /verify-never-runs)
    # tar fails when the archive holds no such member, which is exactly the
    # failure this step exists to catch: an image built without the model.
    docker export "$cid" | tar -x -C "$workdir" "$MODELS_DIR"
    docker rm -f "$cid" >/dev/null
    cid=""
    (cd "${workdir}/${MODELS_DIR}" && sha256sum -c checksums.txt)
    cleanup
  done
done
