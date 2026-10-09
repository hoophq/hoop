#!/usr/bin/env bash
#
# Boots both flavours of Dockerfile.controlplane with a read-only root
# filesystem and default values, against a throwaway PostgreSQL (ENG-602).
#
# It fails when the boot needs a writable path that the image does not
# provide. The session directory is the one expected miss: the boot must log
# the PLUGIN_AUDIT_PATH warning and still answer /api/healthz.
#
# Usage: scripts/controlplane-readonly-e2e.sh   (from the repo root)
set -euo pipefail

for dep in docker curl go tar; do
  command -v "$dep" >/dev/null || { echo "FAIL: missing dependency: $dep" >&2; exit 1; }
done

REPO=$(pwd)
WORK=$(mktemp -d)
RUN_ID="cp-readonly-$$"
NET="$RUN_ID-net"
PG="$RUN_ID-pg"
CP="$RUN_ID-cp"
BOOT_TIMEOUT=${BOOT_TIMEOUT:-90}

cleanup() {
  docker rm -f "$CP" "$PG" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

case "$(docker info --format '{{.Architecture}}')" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) echo "FAIL: unsupported docker architecture" >&2; exit 1 ;;
esac

echo "==> building hoop for linux/$ARCH"
mkdir -p "$WORK/bin" "$WORK/ctx/dist/binaries"
CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -o "$WORK/bin/hoop" ./client
tar -czf "$WORK/ctx/dist/binaries/hoop_e2e_Linux_${ARCH}.tar.gz" -C "$WORK/bin" ./hoop

docker network create "$NET" >/dev/null
docker run -d --name "$PG" --network "$NET" \
  -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=hoop postgres:16-alpine >/dev/null
for _ in $(seq 1 30); do
  docker exec "$PG" pg_isready -U postgres -d hoop >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$PG" pg_isready -U postgres -d hoop >/dev/null || { echo "FAIL: postgres did not start" >&2; exit 1; }

boot_readonly() { # $1=target
  local target=$1 image="hoopcontrolplane:$RUN_ID-$1"
  echo "==> $target: building image"
  docker build -q -f "$REPO/Dockerfile.controlplane" --target "$target" -t "$image" "$WORK/ctx" >/dev/null

  echo "==> $target: booting with --read-only"
  docker rm -f "$CP" >/dev/null 2>&1 || true
  docker run -d --name "$CP" --network "$NET" --read-only -p 127.0.0.1::8009 \
    -e "POSTGRES_DB_URI=postgres://postgres:pw@$PG:5432/hoop?sslmode=disable" \
    -e API_URL=http://127.0.0.1:8009 \
    "$image" >/dev/null
  local port
  port=$(docker port "$CP" 8009/tcp)
  port=${port%%$'\n'*}
  port=${port##*:}

  local i code=000
  for i in $(seq 1 "$BOOT_TIMEOUT"); do
    if [[ $(docker inspect -f '{{.State.Running}}' "$CP") != true ]]; then
      docker logs "$CP" 2>&1 | tail -n 40 >&2
      echo "FAIL: $target: the control plane exited during boot" >&2
      exit 1
    fi
    code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/healthz" || true)
    [[ $code == 200 ]] && break
    sleep 1
  done
  if [[ $code != 200 ]]; then
    docker logs "$CP" 2>&1 | tail -n 40 >&2
    echo "FAIL: $target: /api/healthz did not answer 200 in ${BOOT_TIMEOUT}s" >&2
    exit 1
  fi
  # Read the logs whole: grep -q under pipefail fails on the SIGPIPE it causes.
  local logs
  logs=$(docker logs "$CP" 2>&1)
  if [[ $logs != *'set PLUGIN_AUDIT_PATH to a writable directory'* ]]; then
    echo "FAIL: $target: no warning about the read-only session directory" >&2
    exit 1
  fi
  docker rm -f "$CP" >/dev/null
  docker rmi "$image" >/dev/null 2>&1 || true
  echo "==> $target OK (ready in ${i}s)"
}

boot_readonly distroless
boot_readonly default

echo "PASS: control plane boots on a read-only root filesystem"
