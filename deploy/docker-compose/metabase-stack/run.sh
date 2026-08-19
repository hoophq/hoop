#!/usr/bin/env bash
#
# Brings up the stack:
#
#   1. mint a self-signed cert for appdb
#   2. build the hoop-inspect sidecar image from the local hoopinspect tree
#   3. compose up
#   4. drive Metabase's setup API and register the database THROUGH the sidecar
#
# Step 4 is the only thing ../envoy-stack does not have. Metabase keeps its own
# state, so it has to be told the connection exists, and on OSS the declarative
# config file is Pro-only so the setup API is the supported path.
# ./metabase/provision.py does that and nothing else: no plugin, no patch,
# because the integration genuinely is a hostname and a port.
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
    rm -rf upstream/certs
    exit 0
fi

REBUILD=""
[[ "${1:-}" == "--rebuild" ]] && REBUILD=1

need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }
need docker; need curl; need openssl; need python3

# --------------------------------------------------------------- 0. TLS cert
# One hop is encrypted: sidecar -> appdb. Metabase -> sidecar is plaintext on a
# container network, the shape a sidecar deployment actually has.
#
# The key is chmodded rather than left to the host umask, because postgres
# refuses to start unless it is 0600.
c_step "TLS certificate for appdb"
if [[ ! -f upstream/certs/server.crt ]]; then
    mkdir -p upstream/certs
    openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
        -keyout upstream/certs/server.key -out upstream/certs/server.crt \
        -subj "/CN=appdb" \
        -addext "subjectAltName=DNS:appdb" \
        2>/dev/null
    chmod 600 upstream/certs/server.key
    c_ok "generated upstream/certs/server.crt (CN=appdb)"
else
    c_ok "reusing upstream/certs/server.crt"
fi

# ----------------------------------------------------------- 1. build sidecar
# Same image and same Dockerfile as ../envoy-stack, so building it in either
# stack serves the other.
c_step "hoop-inspect image"
if [[ -n "$REBUILD" ]] || ! docker image inspect hoop-inspect:local >/dev/null 2>&1; then
    docker compose build hoop-inspect
    c_ok "built hoop-inspect:local from ../../../hoopinspect"
else
    c_ok "reusing hoop-inspect:local (./run.sh --rebuild to rebuild)"
fi

# ---------------------------------------------------------------- 2. compose
# Metabase migrates an empty H2 application database on first boot, so --wait
# sits here for a minute or so. Its healthcheck allows for that.
c_step "Starting appdb, hoop-inspect, metabase"
docker compose up -d --wait
c_ok "metabase :3000, sidecar admin :19000, postgres lane :15432"

# -------------------------------------------------------------- 3. provision
c_step "Provisioning Metabase"
python3 ./metabase/provision.py http://localhost:3000

cat <<'EOF'

ready

  Metabase   http://localhost:3000   demo@hoop.dev / hoopdemo1

  Metabase sees this database only through hoop-inspect. OSS has no masking
  of its own, so every redacted cell came from the sidecar. The control is on
  the wire, so dbt, DBeaver, a notebook and psql get the same treatment.

    Browse         http://localhost:3000/browse/databases
    SQL editor     http://localhost:3000/question
                   Pick the database above, then: SELECT * FROM customers;

  The ground truth, straight from the database with the proxy bypassed:

      docker compose exec -T client env PGPASSWORD=apppass psql \
        -h appdb -p 5432 -U appuser -d appdb -c 'SELECT * FROM customers;'

  Or just: ./demo.sh

  Audit trail, every statement Metabase ran, including its own sync:

      curl -s localhost:19000/api/sessions | python3 -m json.tool
      docker compose logs -f hoop-inspect

  Teardown:
      ./run.sh down

EOF
