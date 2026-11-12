#!/usr/bin/env bash
#
# Brings the stack up with the reload overlay: the sidecar reads its config
# from reload/work/, a directory this script seeds from sidecar/config.yaml.
#
#   ./reload/run.sh          bring everything up on a fresh copy of the config
#   ./reload/run.sh down     tear down, including volumes, and drop the copy
#
# Certificates and the image come from ../run.sh, which this calls first.

set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE=(docker compose -f docker-compose.yml -f reload/docker-compose.reload.yml)

if [[ "${1:-}" == "down" ]]; then
    "${COMPOSE[@]}" down -v --remove-orphans
    rm -rf reload/work
    exit 0
fi

mkdir -p reload/work
cp sidecar/config.yaml reload/work/config.yaml

# ../run.sh mints the certs and builds the image, then runs `compose up` on
# the base files; the overlay's up below replaces the sidecar container with
# the one that reads reload/work/.
./run.sh "$@"
"${COMPOSE[@]}" up -d --wait hoop-inspect

cat <<'EOF'

ready: the sidecar reads reload/work/config.yaml and re-reads it on change.

  ./reload/demo.sh     add a lane, use it, remove it, watch a bind conflict refused
EOF
