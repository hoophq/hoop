#!/usr/bin/env bash
#
# Brings up the stack:
#
#   1. build the hoop-inspect sidecar image from the local sidecar tree
#   2. compose up
#
# A one-shot compose service mints the MySQL CA and server certificate into a
# named volume. MySQL requires secure transport; hoop-inspect verifies that CA.
#
# Usage:
#   ./run.sh              bring everything up
#   ./run.sh --rebuild    force a sidecar image rebuild first
#   ./run.sh down         tear down including volumes

set -euo pipefail
cd "$(dirname "$0")"

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

if [[ "${1:-}" == "down" ]]; then
    docker compose down -v --remove-orphans
    exit 0
fi

REBUILD=""
[[ "${1:-}" == "--rebuild" ]] && REBUILD=1

need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }
need docker; need curl; need python3

# ------------------------------------------------------------ 1. build sidecar
# The image is the one ../envoy-stack builds, from ../../../sidecar, so a
# library change is one rebuild away from running in either stack. Skipped
# when the image already exists unless --rebuild says otherwise.
c_step "hoop-inspect image"
if [[ -n "$REBUILD" ]] || ! docker image inspect hoop-inspect:local >/dev/null 2>&1; then
    docker compose build hoop-inspect
    c_ok "built hoop-inspect:local from ../../../sidecar"
else
    c_ok "reusing hoop-inspect:local (./run.sh --rebuild to rebuild)"
fi

# ----------------------------------------------------------------- 2. compose
c_step "Starting appdb, hoop-inspect, envoy, client"
docker compose up -d --wait
c_ok "mysql :3307, sidecar admin :19000, envoy admin :9901"

cat <<'EOF'

ready

  mysql (Envoy :3307 -> hoop-inspect :13306 -> TLS -> appdb):

    docker compose exec -T client env MYSQL_PWD=apppass \
      mysql -h envoy -P 3306 -u appuser appdb \
            -e 'SELECT name, email FROM customers'

  The client uses plaintext so hoop-inspect can inspect it. The relay removes
  CLIENT_SSL from the client greeting and originates verified TLS to appdb.

  Or just: ./demo.sh

  Audit trail -- every statement above:

    curl -s localhost:19000/api/sessions | python3 -m json.tool
    docker compose logs -f hoop-inspect

  Teardown:
    ./run.sh down

EOF
