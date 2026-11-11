#!/usr/bin/env bash
#
# Brings up the MSSQL + Kerberos lane of the envoy-stack.
#
#   ./mssql-stack.sh            mint certs, build, start, wait for readiness
#   ./mssql-stack.sh --rebuild  rebuild the sidecar image first
#   ./mssql-stack.sh down       tear down, including volumes and certs
#   ./mssql-stack.sh logs [svc] tail logs
#   ./mssql-stack.sh psql       open a psql session on the postgres lane
#
# The certificates are the fiddly part and the reason this script exists
# rather than a bare `docker compose up`. Encrypt=strict FORBIDS
# TrustServerCertificate, so unlike every other lane in this stack the client
# genuinely validates the chain, which means a real CA has to sign two server
# certificates: one for Envoy (which the client sees) and one for SQL Server
# (which the relay sees). Both are issued for mssql.hoop.test, because that is
# the name the Kerberos SPN is built from.
#
# SQL Server publishes no arm64 image, so on Apple Silicon it runs emulated
# and first boot takes a few minutes.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STACK="$(cd "$HERE/../../../deploy/docker-compose/envoy-stack" && pwd)"
CERTS="$STACK/mssql/certs"

COMPOSE=(-f docker-compose.yml -f mssql/docker-compose.mssql.yml)

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
c_warn() { printf '\033[33mwarn\033[0m %s\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

cd "$STACK"

case "${1:-up}" in
    down)
        docker compose "${COMPOSE[@]}" down -v --remove-orphans
        rm -rf "$CERTS"
        c_ok "torn down; certs removed"
        exit 0
        ;;
    logs)
        shift
        exec docker compose "${COMPOSE[@]}" logs -f "$@"
        ;;
    psql)
        # Envoy terminates this lane's TLS, so sslmode=require works. The two
        # extra parameters are not optional here:
        #
        #   channel_binding=disable  the relay strips SCRAM-SHA-256-PLUS
        #     (it terminated the UPSTREAM TLS, so the client cannot bind to a
        #     channel it never saw). A client on TLS expects that mechanism to
        #     be offered and treats its absence as a downgrade attack.
        #
        #   gssencmode=disable  libpq asks for GSS encryption first whenever a
        #     ticket is in the cache. Envoy's postgres filter reacts to that
        #     request by entering a terminal state where it stops terminating
        #     anything, so the TLS handshake that follows never happens.
        #
        # Both fail loudly when omitted, which is the right direction.
        shift
        exec docker compose "${COMPOSE[@]}" exec -T client env PGPASSWORD=apppass \
            psql "host=envoy port=5432 dbname=appdb user=appuser sslmode=require channel_binding=disable gssencmode=disable" "$@"
        ;;
    up|--rebuild) ;;
    *) die "unknown command: $1" ;;
esac

REBUILD=""
[[ "${1:-}" == "--rebuild" ]] && REBUILD=1

need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }
need docker; need openssl

# ------------------------------------------------------------ 0. base certs
# The postgres and https lanes need what run.sh mints. Reuse it rather than
# duplicating the logic, so the two paths cannot drift.
if [[ ! -f envoy/certs/server.crt || ! -f upstream/certs/server.crt ]]; then
    c_step "Base stack certificates (delegating to run.sh)"
    # run.sh mints certs and then brings the base stack up; the compose call
    # below reconciles it to the overlay, so the extra `up` is harmless.
    ./run.sh >/dev/null
    c_ok "base certificates minted"
fi

# ------------------------------------------------------------- 1. TLS chain
c_step "TLS chain for the MSSQL lane"
if [[ -f "$CERTS/ca.crt" ]]; then
    c_ok "reusing $CERTS"
else
    mkdir -p "$CERTS"

    # A real CA, because Encrypt=strict will not accept a self-signed leaf the
    # way -C does on every other lane.
    openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
        -keyout "$CERTS/ca.key" -out "$CERTS/ca.crt" \
        -subj "/CN=hoop-demo-ca" 2>/dev/null

    # Two leaves, one name. The client validates Envoy's; the relay validates
    # SQL Server's. Both are mssql.hoop.test because that string is what the
    # SPN is derived from, and a mismatch there breaks Kerberos rather than
    # TLS — a failure that reads as "login failed" with nothing about names.
    for leaf in envoy-mssql mssql; do
        openssl req -newkey rsa:2048 -nodes \
            -keyout "$CERTS/$leaf.key" -out "$CERTS/$leaf.csr" \
            -subj "/CN=mssql.hoop.test" 2>/dev/null
        openssl x509 -req -in "$CERTS/$leaf.csr" \
            -CA "$CERTS/ca.crt" -CAkey "$CERTS/ca.key" -CAcreateserial \
            -out "$CERTS/$leaf.crt" -days 365 \
            -extfile <(printf 'subjectAltName=DNS:mssql.hoop.test,DNS:mssql\nextendedKeyUsage=serverAuth\n') \
            2>/dev/null
        rm -f "$CERTS/$leaf.csr"
    done

    # SQL Server wants PKCS#8. openssl genrsa-style keys from `req -newkey`
    # are already PKCS#8 here, but normalizing makes that explicit rather
    # than incidental.
    openssl pkcs8 -topk8 -nocrypt -in "$CERTS/mssql.key" -out "$CERTS/mssql.pk8" 2>/dev/null
    mv "$CERTS/mssql.pk8" "$CERTS/mssql.key"

    chmod 644 "$CERTS"/*.crt
    chmod 600 "$CERTS"/*.key
    c_ok "minted CA + envoy-mssql + mssql leaves (CN=mssql.hoop.test)"
fi

# --------------------------------------------------------------- 2. images
# ALWAYS rebuild the sidecar. This is a dev script whose whole purpose is
# exercising local codec changes, and the base run.sh's reuse-by-default
# turns an edited codec into "unsupported protocol" at startup — a message
# that points at the config rather than at the stale image actually causing
# it. Docker's layer cache makes the rebuild nearly free when nothing moved.
c_step "Images"
BUILD_ARGS=()
[[ -n "$REBUILD" ]] && BUILD_ARGS+=(--no-cache)
docker compose "${COMPOSE[@]}" build "${BUILD_ARGS[@]}" hoop-inspect
c_ok "built hoop-inspect:local from ../../../sidecar"
docker compose "${COMPOSE[@]}" build "${BUILD_ARGS[@]}" samba sqlclient >/dev/null
c_ok "samba-dc + sqlclient images ready"

# --------------------------------------------------------------- 3. compose
c_step "Starting the stack"
c_warn "SQL Server has no arm64 image; on Apple Silicon first boot takes a few minutes"
# mssql-certs is deliberately absent from this list. It is a run-to-completion
# container, and `--wait` reads "exited 0" as a failed dependency; mssql's own
# depends_on already gates on it having succeeded.
docker compose "${COMPOSE[@]}" up -d --wait --wait-timeout 600 \
    samba hoop-inspect opa envoy sqlclient client httpbin

c_step "Seeding SQL Server and creating the Kerberos login"
c_warn "first boot of an emulated SQL Server can take several minutes"
docker compose "${COMPOSE[@]}" up -d mssql
docker compose "${COMPOSE[@]}" up mssql-init --exit-code-from mssql-init \
    || die "seed failed; inspect with: $0 logs mssql"
c_ok "appdb.dbo.customers seeded, alice@HOOP.TEST granted"

cat <<'EOF'

ready

  verify kerberos end to end
    sidecar/scripts/dev/mssql-kerberos-check.sh

  a session by hand, from the client container
    cd deploy/docker-compose/envoy-stack
    CF=(-f docker-compose.yml -f mssql/docker-compose.mssql.yml)
    docker compose "${CF[@]}" exec sqlclient bash -lc \
      'echo alicepass | kinit alice@HOOP.TEST && \
       sqlcmd -S mssql.hoop.test,1433 -E -N strict -d appdb -Q "SELECT name FROM customers"'

  ports
    1433  envoy, TDS 8.0 -> hoop-inspect -> mssql
    8443  envoy https, 5433 envoy postgres, 19000 sidecar admin, 9901 envoy admin
EOF
