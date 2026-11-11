#!/usr/bin/env bash
#
# Brings up two upstreams and the relay in front of them.
#
#   postgres :55432  <- relay :15432   (appdb lane)
#   httpbin  :58080  <- relay :18080   (api lane)
#                       relay :19000   admin
#
# The relay runs as a background process rather than a container, so its audit
# stream lands in a file you can grep and the binary is the one you just built.
#
# Usage:
#   ./02-run.sh          bring it up
#   ./02-run.sh down     tear it down

set -uo pipefail
cd "$(dirname "$0")"
source ./lib.sh

# ---------------------------------------------------------------- teardown
if [[ "${1:-}" == "down" ]]; then
    h "Tearing down"
    if [[ -f "$RELAY_PID" ]]; then
        kill "$(cat "$RELAY_PID")" 2>/dev/null && ok "stopped the relay"
        rm -f "$RELAY_PID"
    fi
    docker rm -f hoop-test-pg hoop-test-http >/dev/null 2>&1 && ok "removed containers"
    note "logs kept in $WORKDIR"
    exit 0
fi

[[ -f "$CONFIG" ]] || die "no config. Run ./01-validate.sh first."
[[ -x "$BIN" ]]    || die "no binary. Run ./01-validate.sh first."

# --------------------------------------------------------------- upstreams
h "Upstreams"

docker rm -f hoop-test-pg hoop-test-http >/dev/null 2>&1 || true

docker run -d --name hoop-test-pg \
    -p "127.0.0.1:${PG_UPSTREAM_PORT}:5432" \
    -e POSTGRES_USER=testuser -e POSTGRES_PASSWORD=testpass -e POSTGRES_DB=appdb \
    postgres:17 >/dev/null || die "could not start postgres"
ok "postgres on :${PG_UPSTREAM_PORT}"

docker run -d --name hoop-test-http \
    -p "127.0.0.1:${HTTP_UPSTREAM_PORT}:80" \
    --entrypoint gunicorn kennethreitz/httpbin \
    -b 0.0.0.0:80 -k gevent --keep-alive 75 httpbin:app >/dev/null \
    || die "could not start httpbin"
ok "httpbin on :${HTTP_UPSTREAM_PORT} (keep-alive 75s)"
note "gunicorn defaults to keep_alive=2s. The relay dials the upstream when it"
note "accepts, then holds the request while it classifies, so a 4s Vertex call"
note "outlives a 2s idle budget and the upstream hangs up first. See the"
note "known limit in README.md."

note "waiting for postgres to accept connections"
for i in $(seq 1 40); do
    if docker exec hoop-test-pg pg_isready -U testuser -d appdb >/dev/null 2>&1; then
        ok "postgres is ready"; break
    fi
    [[ $i -eq 40 ]] && die "postgres never became ready. docker logs hoop-test-pg"
    sleep 0.5
done

# Seed. The ssn column is the one alcatraz refuses as an obvious fixture, so
# it also demonstrates why column rules exist beside entity rules.
seed_postgres hoop-test-pg || die "could not seed postgres. docker logs hoop-test-pg"
ok "seeded 2 customers"

# ------------------------------------------------------------------- relay
h "Relay"

if [[ -f "$RELAY_PID" ]] && kill -0 "$(cat "$RELAY_PID")" 2>/dev/null; then
    kill "$(cat "$RELAY_PID")"; sleep 1
fi

: > "$RELAY_LOG"
"$BIN" -config "$CONFIG" >> "$RELAY_LOG" 2>&1 &
echo $! > "$RELAY_PID"

for i in $(seq 1 40); do
    if curl -sf "http://127.0.0.1:${ADMIN_PORT}/healthz" >/dev/null 2>&1; then
        ok "relay healthy on :${ADMIN_PORT} (pid $(cat "$RELAY_PID"))"; break
    fi
    if ! kill -0 "$(cat "$RELAY_PID")" 2>/dev/null; then
        printf '\n'; cat "$RELAY_LOG"; die "the relay exited during startup"
    fi
    [[ $i -eq 40 ]] && { cat "$RELAY_LOG"; die "the relay never became healthy"; }
    sleep 0.5
done

h "What each lane resolved to"
admin /config | python3 -m json.tool

h "Up"
note "audit stream: tail -f $RELAY_LOG"
note "next:         ./03-verify.sh"
note "teardown:     ./02-run.sh down"
