#!/usr/bin/env bash
# Check that a hoophq/hoopsidecar image gives its user a home it can write.
#
# A free-plan `hoop start sidecar` saves its login in ~/.hoop/sidecar. When
# HOME is missing or not writable, the login is approved and then lost, and
# the web app keeps a sidecar no pod can use (DES-251).
#
# The distroless flavours have no shell, so a static busybox from
# BUSYBOX_IMAGE is mounted read-only and used as the entrypoint. The image's
# own USER and HOME apply: no --user, no -e HOME.
#
# Usage: check-sidecar-image-home.sh <image-ref> [<image-ref> ...]
# PLATFORMS (space separated) defaults to "linux/amd64 linux/arm64".

set -euo pipefail

BUSYBOX_IMAGE=${BUSYBOX_IMAGE:-busybox:1.37.0-musl}
read -r -a PLATFORMS <<< "${PLATFORMS:-linux/amd64 linux/arm64}"

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <image-ref> [<image-ref> ...]" >&2
  exit 2
fi

cid=""
probe=""
cleanup() {
  if [ -n "$cid" ]; then docker rm -f "$cid" >/dev/null 2>&1 || true; fi
  if [ -n "$probe" ]; then rm -rf "$probe"; fi
}
trap cleanup EXIT

# The script the probe runs. It fails when HOME is unset or /, when 10001 is
# not the user, or when the state folder cannot hold a 0600 file.
# shellcheck disable=SC2016
check='
set -e
b=/probe/busybox
uid=$($b id -u)
[ "$uid" = 10001 ] || { echo "uid is $uid, want 10001"; exit 1; }
[ -n "$HOME" ] && [ "$HOME" != / ] || { echo "HOME is \"$HOME\""; exit 1; }
dir="$HOME/.hoop/sidecar"
$b mkdir -p "$dir"
umask 077
echo "{}" > "$dir/credentials.json"
$b rm "$dir/credentials.json"
echo "HOME=$HOME writable by uid $uid"
'

failed=0
for platform in "${PLATFORMS[@]}"; do
  probe=$(mktemp -d)
  cid=$(docker create --platform "$platform" "$BUSYBOX_IMAGE" /bin/true)
  docker cp "$cid:/bin/busybox" "$probe/busybox" >/dev/null
  docker rm -f "$cid" >/dev/null
  cid=""
  chmod 0755 "$probe" "$probe/busybox"

  for image in "$@"; do
    echo "checking ${image} (${platform})"
    # The image must set HOME itself (ENV HOME). Docker fills HOME from
    # /etc/passwd at run time, other runtimes may not; the container's
    # Config.Env holds only what the image declares.
    cid=$(docker create --platform "$platform" "$image" /check-never-runs)
    declared=$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$cid" | grep '^HOME=' || true)
    docker rm -f "$cid" >/dev/null
    cid=""
    if [ -z "$declared" ]; then
      echo "FAIL: ${image} (${platform}) does not set ENV HOME" >&2
      failed=1
      continue
    fi
    if ! docker run --rm --platform "$platform" \
        -v "$probe:/probe:ro" --entrypoint /probe/busybox \
        "$image" sh -c "$check"; then
      echo "FAIL: ${image} (${platform}) has no writable home" >&2
      failed=1
    fi
  done
  rm -rf "$probe"
  probe=""
done

exit "$failed"
