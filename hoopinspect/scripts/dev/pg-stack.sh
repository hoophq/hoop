#!/usr/bin/env bash
#
# Brings up the Postgres + Kerberos lane.
#
#   ./pg-stack.sh            build, start, wait
#   ./pg-stack.sh down       tear down, including volumes
#   ./pg-stack.sh logs [svc] tail logs
#   ./pg-stack.sh psql       a password session through the relay
#   ./pg-stack.sh kinit      a Kerberos session through the relay
#
# The same realm and relay as mssql-stack.sh, without SQL Server. That is the
# reason it exists: SQL Server publishes no arm64 image, so on Apple Silicon it
# runs emulated and adds minutes to every iteration. This one starts in
# seconds, and it is the lane where a Kerberos login actually succeeds.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STACK="$(cd "$HERE/../../../deploy/docker-compose/envoy-stack" && pwd)"
cd "$STACK"

COMPOSE=(-f docker-compose.yml -f postgres/docker-compose.postgres.yml)
dc() { docker compose "${COMPOSE[@]}" "$@"; }

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

# Envoy terminates this lane's TLS, so sslmode=require works. The two extra
# parameters are not decoration:
#
#   channel_binding=disable  the relay strips SCRAM-SHA-256-PLUS, because it
#     terminated the UPSTREAM TLS and the client cannot bind to a channel it
#     did not see. A client on TLS expects that mechanism to be offered and
#     reads its absence as a downgrade attack.
#
#   gssencmode=disable  libpq asks for GSS encryption first whenever a ticket
#     is in the cache. Envoy's postgres filter treats that request as "this
#     session is encrypted", enters a state it does not leave, and stops
#     terminating anything, the TLS handshake included.
#
# Both fail loudly when omitted.
PGOPTS="sslmode=require channel_binding=disable gssencmode=disable"

case "${1:-up}" in
    down)
        dc down -v --remove-orphans
        c_ok "torn down"
        exit 0
        ;;
    logs)
        # `exec dc ...` cannot work: dc is a shell function and exec replaces
        # the process with a FILE. Call docker compose directly here.
        shift; exec docker compose "${COMPOSE[@]}" logs -f "$@"
        ;;
    psql)
        shift
        exec docker compose "${COMPOSE[@]}" exec -T client env PGPASSWORD=apppass \
            psql "host=envoy port=5432 dbname=appdb user=appuser $PGOPTS" "$@"
        ;;
    kinit)
        shift
        docker compose "${COMPOSE[@]}" exec -T client \
            sh -c 'echo alicepass | kinit alice@HOOP.TEST >/dev/null 2>&1'
        exec docker compose "${COMPOSE[@]}" exec -T client \
            psql "host=appdb.hoop.test port=5432 dbname=appdb user=alice@HOOP.TEST $PGOPTS" "$@"
        ;;
    up) ;;
    *) die "unknown command: $1" ;;
esac

need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }
need docker; need openssl

# ------------------------------------------------------------- 0. base certs
if [[ ! -f envoy/certs/server.crt || ! -f upstream/certs/server.crt ]]; then
    c_step "Base stack certificates (delegating to run.sh)"
    ./run.sh >/dev/null
    c_ok "base certificates minted"
fi

# ---------------------------------------------------------------- 1. images
# ALWAYS rebuild the relay. This script exists to exercise local codec
# changes, and reuse-by-default turns an edited codec into a puzzling startup
# error. Docker's layer cache keeps it cheap.
c_step "Images"
dc build hoop-inspect
dc build samba >/dev/null
c_ok "hoop-inspect and samba-dc ready"

# --------------------------------------------------------------- 2. compose
c_step "Starting the stack"
dc up -d --wait --wait-timeout 300 samba appdb hoop-inspect opa envoy client httpbin
c_ok "up"

cat <<EOF

ready

  verify the flow
    hoopinspect/scripts/dev/pg-kerberos-check.sh

  a password session
    hoopinspect/scripts/dev/pg-stack.sh psql -c 'SELECT * FROM customers'

  a Kerberos session
    hoopinspect/scripts/dev/pg-stack.sh kinit -c 'SELECT current_user'

  ports
    5433  envoy postgres (host) -> hoop-inspect -> appdb
    19000 sidecar admin, 9901 envoy admin
EOF
