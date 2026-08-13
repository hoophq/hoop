#!/usr/bin/env bash
#
# Brings up the SQL Server 2019 lane: TDS 7.4, login-only encryption, no Envoy.
#
#   ./mssql2019-stack.sh            build, start, seed
#   ./mssql2019-stack.sh --rebuild  no-cache rebuild of the sidecar first
#   ./mssql2019-stack.sh down       tear down, including volumes
#   ./mssql2019-stack.sh logs [svc] tail logs
#   ./mssql2019-stack.sh sql        open sqlcmd against the relay
#
# No certificates to mint, unlike the 2022 lane. That stack needs a chain
# because Encrypt=strict forbids TrustServerCertificate; this one runs SQL
# Server with no TLS configuration at all, which is the case under test.
# SQL Server mints its own fallback certificate at startup and encrypts the
# login with it regardless.
#
# SQL Server publishes no arm64 image, so on Apple Silicon it runs emulated
# and first boot takes several minutes.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STACK="$(cd "$HERE/../../../deploy/docker-compose/envoy-stack/mssql2019" && pwd)"

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
c_warn() { printf '\033[33mwarn\033[0m %s\n' "$*"; }

cd "$STACK" || exit 1
dc() { docker compose -f docker-compose.mssql2019.yml "$@"; }

case "${1:-up}" in
    down)
        c_step "Tearing down"
        dc down -v --remove-orphans
        c_ok "torn down"
        exit 0
        ;;
    logs)
        shift
        dc logs -f "$@"
        exit 0
        ;;
    sql)
        # -No: Encrypt=Optional, which negotiates ENCRYPT_OFF. -C trusts the
        # server's self-signed fallback certificate.
        exec dc exec sqlclient sqlcmd \
            -S hoop-inspect,11433 -U appuser -P 'App!Passw0rd' -No -C -d appdb
        ;;
    --rebuild)
        c_step "Rebuilding the sidecar image (no cache)"
        dc build --no-cache hoop-inspect || exit 1
        ;;
esac

# ------------------------------------------------------------------ images
c_step "Images"
# ALWAYS rebuild the sidecar. Reusing a stale one turns an edited codec into a
# lane that behaves like the old binary, and the symptom (statements missing
# from the audit trail) reads as a protocol bug rather than a stale image.
# Docker's layer cache keeps this cheap.
dc build hoop-inspect || exit 1
c_ok "built hoop-inspect:local from hoopinspect/"
dc build sqlclient || exit 1
c_ok "sqlclient image ready"

# ------------------------------------------------------------------ start
c_step "Starting SQL Server 2019 and the relay"
c_warn "no arm64 image exists; on Apple Silicon first boot takes several minutes"
dc up -d --wait --wait-timeout 900 mssql2019 hoop-inspect sqlclient || {
    c_warn "one or more services failed to become healthy"
    dc ps
    exit 1
}
c_ok "sql server 2019 and hoop-inspect are healthy"

# ------------------------------------------------------------------- seed
c_step "Seeding appdb"
dc up mssql2019-init --exit-code-from mssql2019-init || {
    c_warn "seeding failed"
    exit 1
}
c_ok "appdb.dbo.customers seeded, appuser created"

c_step "Ready"
cat <<'EOF'
  Verify:      hoopinspect/scripts/dev/mssql2019-check.sh
  A session:   hoopinspect/scripts/dev/mssql2019-stack.sh sql
  Audit API:   curl -s localhost:19001/api/sessions | jq
  Tear down:   hoopinspect/scripts/dev/mssql2019-stack.sh down

  The lane, by hand:
    cd deploy/docker-compose/envoy-stack/mssql2019
    docker compose -f docker-compose.mssql2019.yml exec sqlclient \
      sqlcmd -S hoop-inspect,11433 -U appuser -P 'App!Passw0rd' -No -C \
             -d appdb -Q "SELECT name FROM customers"
EOF
