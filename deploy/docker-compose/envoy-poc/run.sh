#!/usr/bin/env bash
#
# Brings up the POC:
#
#   1. mint a self-signed cert for Envoy
#   2. build the hoop-inspect sidecar image from the local hoopinspect tree
#   3. compose up
#
# There is no hoop gateway, no agent and no control-plane database here. The
# sidecar reads one JSON file and needs no API calls to configure, so the
# whole bring-up is compose plus a certificate.
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
    rm -rf envoy/certs upstream/certs
    exit 0
fi

REBUILD=""
[[ "${1:-}" == "--rebuild" ]] && REBUILD=1

need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }
need docker; need curl; need openssl; need python3

# --------------------------------------------------------------- 0. TLS certs
# Two independent hops, two certs.
#
# Envoy terminates the client's TLS; every client below uses -k.
c_step "TLS certificate for Envoy"
if [[ ! -f envoy/certs/server.crt ]]; then
    mkdir -p envoy/certs
    openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
        -keyout envoy/certs/server.key -out envoy/certs/server.crt \
        -subj "/CN=localhost" \
        -addext "subjectAltName=DNS:localhost,DNS:envoy,IP:127.0.0.1" \
        2>/dev/null
    c_ok "generated envoy/certs/server.crt"
else
    c_ok "reusing envoy/certs/server.crt"
fi

# appdb terminates the SIDECAR's TLS. This is the hop that used to be
# plaintext: hoop-inspect now speaks pgwire StartTLS to the database, so the
# bytes between the two containers are encrypted while the sidecar still sees
# decrypted statements and can mask the rows coming back.
#
# The key must be owned by the postgres uid (999) and mode 0600, or the server
# refuses to start. Copying it into a file we chown here avoids depending on
# whatever the host's umask happens to be.
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

# ------------------------------------------------------------ 1. build sidecar
# The image is built from ../../../hoopinspect, so a library change is one
# rebuild away from running. Skipped when the image already exists unless
# --rebuild says otherwise.
c_step "hoop-inspect image"
if [[ -n "$REBUILD" ]] || ! docker image inspect hoop-inspect:local >/dev/null 2>&1; then
    docker compose build hoop-inspect
    c_ok "built hoop-inspect:local from ../../../hoopinspect"
else
    c_ok "reusing hoop-inspect:local (./run.sh --rebuild to rebuild)"
fi

# ----------------------------------------------------------------- 2. compose
c_step "Starting upstreams, hoop-inspect, opa, envoy"
docker compose up -d --wait
c_ok "https :8443, postgres :5433, sidecar admin :19000, envoy admin :9901"

cat <<'EOF'

ready

  Tier 1 -- fat gate (Envoy + OPA). Reachability only.

    allowed:
      curl -k https://localhost:8443/json -H 'X-Hoop-User: alice'

    denied by OPA (bob has no grant; never reaches hoop-inspect):
      curl -k https://localhost:8443/json -H 'X-Hoop-User: bob' -i

    denied by OPA (no identity at all):
      curl -k https://localhost:8443/json -i

  Tier 2 -- deep inspection (hoop-inspect). Envoy forwarded bytes it cannot
  judge; only the sidecar can read them.

    postgres (Envoy :5433 -> hoop-inspect :15432 -> appdb):
      docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable \
        psql -h envoy -p 5432 -U appuser -d appdb \
             -c 'SELECT name, email FROM customers;'

    Or just: ./demo.sh

  Audit trail -- every statement above:

      curl -s localhost:19000/api/sessions | python3 -m json.tool
      docker compose logs -f hoop-inspect

  Teardown:
      ./run.sh down

EOF
