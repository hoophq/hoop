#!/usr/bin/env bash
#
# Brings up the stack:
#
#   1. mint a self-signed cert for Envoy
#   2. build the hoop-inspect sidecar image from the local hoopinspect tree
#   3. compose up
#
# There is no hoop gateway, no agent and no control-plane database here. The
# sidecar reads one JSON file and needs no API calls to configure, so the
# whole bring-up is compose plus a certificate.
#
# --review is the one exception, and it is opt-in for that reason: the
# human-approval gate needs somewhere for a review to live and someone to
# answer it, so it adds a gateway, a database and a model credential. See
# ./demo-review.sh.
#
# Usage:
#   ./run.sh              bring everything up
#   ./run.sh --review     also bring up the gateway and the approval gate
#   ./run.sh --rebuild    force a sidecar image rebuild first
#   ./run.sh down         tear down including volumes

set -euo pipefail
cd "$(dirname "$0")"

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

if [[ "${1:-}" == "down" ]]; then
    docker compose --profile review down -v --remove-orphans
    rm -rf envoy/certs upstream/certs gateway/bin
    exit 0
fi

REBUILD=""
REVIEW=""
for arg in "$@"; do
    case "$arg" in
        --rebuild) REBUILD=1 ;;
        --review)  REVIEW=1 ;;
        *) die "unknown argument: $arg (try --review, --rebuild or down)" ;;
    esac
done

# The sidecar picks its lane set from this, interpolated into the config
# volume in docker-compose.yml.
export INSPECT_CONFIG="config.yaml"
COMPOSE=(docker compose)
if [[ -n "$REVIEW" ]]; then
    INSPECT_CONFIG="config.review.yaml"
    COMPOSE=(docker compose --profile review)
fi
# Both profiles, always. review-bootstrap depends_on hoop-gateway, and compose
# validates the whole project before it honours --no-deps: naming only the
# bootstrap profile leaves that dependency undefined and the project invalid.
BOOTSTRAP=(docker compose --profile review --profile review-bootstrap)

need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }
need docker; need curl; need openssl; need python3
[[ -n "$REVIEW" ]] && need go

# ------------------------------------------------------- 0. review prereqs
# Checked FIRST, before anything is built or started: a missing model
# credential is the most likely reason --review does not work, and finding
# that out after a two minute bring-up is the worst place to learn it.
if [[ -n "$REVIEW" ]]; then
    c_step "review lane prerequisites"
    # A .env beside this script is read if present, matching the other compose
    # stacks in this repo. Exported keys in the shell win over it.
    if [[ -f .env ]]; then
        set -a; . ./.env; set +a
        c_ok "read ./.env"
    fi
    if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
        die "$(cat <<'MSG'
--review needs a model credential.

  require_review is an ai_analysis ACTION: the analyzer is what decides which
  statements are worth a human, so the gate cannot run without one. The
  trigger is narrow (delete and update on one lane), so a full walk of
  ./demo-review.sh costs a handful of classifications.

  export ANTHROPIC_API_KEY=sk-ant-...
  ./run.sh --review

  Using a different provider: set its key here and change `provider:` and
  `model:` in hoopinspect/config.review.yaml. Every provider the binary links
  (anthropic, openai, vertex) works; only the credential plumbing below is
  wired for Anthropic.
MSG
)"
    fi
    c_ok "ANTHROPIC_API_KEY is set"
fi

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
#
# --review always rebuilds. The approval gate is a library change, so an image
# cached from before it exists comes up with "unknown field review" and points
# at the config rather than at the stale image. Docker's layer cache makes the
# rebuild cheap when the source has not moved.
c_step "hoop-inspect image"
if [[ -n "$REBUILD" || -n "$REVIEW" ]] || ! docker image inspect hoop-inspect:local >/dev/null 2>&1; then
    docker compose build hoop-inspect
    c_ok "built hoop-inspect:local from ../../../hoopinspect"
else
    c_ok "reusing hoop-inspect:local (./run.sh --rebuild to rebuild)"
fi

# ------------------------------------------------------------ 1b. gateway
# Cross-compiled on the host rather than inside a container: building it in
# Docker would mean shipping the whole repository as a build context to
# compile one Go binary, and would throw away the host's build cache each run.
if [[ -n "$REVIEW" ]]; then
    c_step "hoop gateway binary"
    case "$(docker info --format '{{.Architecture}}' 2>/dev/null)" in
        aarch64|arm64) GOARCH=arm64 ;;
        x86_64|amd64)  GOARCH=amd64 ;;
        *) die "could not determine the docker host architecture" ;;
    esac
    mkdir -p gateway/bin
    ( cd ../../.. && env CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" \
        go build -ldflags "-s -w" \
        -o deploy/docker-compose/envoy-stack/gateway/bin/hoop client/hoop.go )
    c_ok "built gateway/bin/hoop (linux/$GOARCH) from the local tree"

    c_step "hoop-gateway image"
    "${COMPOSE[@]}" build hoop-gateway
    c_ok "built hoop-gateway:local"
fi

# ----------------------------------------------------------------- 2. compose
#
# The review lane comes up in three phases, because the sidecar refuses to
# start without its credential and the credential does not exist until the
# gateway is answering. Compose cannot express "run this one-shot in between",
# so the script does.
if [[ -n "$REVIEW" ]]; then
    c_step "Starting the gateway and its database"
    "${COMPOSE[@]}" up -d --wait gateway-db hoop-gateway
    c_ok "gateway API on :8009"

    c_step "Bootstrapping the review lane"
    # The key is handed over through a named volume rather than a bind mount:
    # the sidecar runs as uid 10001 and refuses a credential file anything
    # else can read, which a host bind mount cannot promise on every platform.
    printf '%s' "$ANTHROPIC_API_KEY" | "${BOOTSTRAP[@]}" run --rm --no-deps -T \
        --entrypoint /bin/sh review-bootstrap \
        -c 'cat > /analyzer/api-key && chmod 600 /analyzer/api-key && chown 10001 /analyzer/api-key'
    c_ok "model credential staged (0600, uid 10001)"

    "${BOOTSTRAP[@]}" run --rm review-bootstrap
fi

c_step "Starting upstreams, hoop-inspect, opa, envoy"
"${COMPOSE[@]}" up -d --wait
c_ok "https :8443, postgres :5433, sidecar admin :19000, envoy admin :9901"

if [[ -n "$REVIEW" ]]; then
    cat <<'EOF'

ready (review lane)

  A statement the analyzer flags is REFUSED and the database session ends.
  Nothing is held open: approve it, then reconnect and re-issue.

    ./demo-review.sh          the whole flow, narrated

  By hand:

    # 1. refused, and a review is filed
    docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable \
      psql -h envoy -p 5432 -U appuser -d appdb \
           -c 'DELETE FROM customers WHERE id = 1;'

    # 2. answer it as the reviewer (admin@hoop.dev / hoopdemo123)
    curl -s localhost:8009/api/reviews | python3 -m json.tool

    # 3. re-issue the same statement -- it runs, exactly once

  Gate counters, beside the lane's own:
      curl -s localhost:19000/stats | python3 -m json.tool

  Teardown:
      ./run.sh down

EOF
    exit 0
fi

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
