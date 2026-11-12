#!/usr/bin/env bash
#
# Adds a listener to a running sidecar, uses it, removes it, and shows the
# sidecar refuse a listener that cannot bind, with no restart and with a
# session on another lane open the whole time.
#
# Prereqs: ./reload/run.sh
#
# The sidecar stats its config file every ten seconds; the demo sends SIGHUP
# instead of waiting, which is the same read. Under a control plane the
# heartbeat delivers the edit and the outcome is the same.
#
# Exit status is non-zero if any check fails.

set -uo pipefail
cd "$(dirname "$0")/.."

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
ok()   { printf '\033[32m  ok\033[0m    %s\n' "$*"; }
fail() { printf '\033[31m  FAIL\033[0m  %s\n' "$*"; FAILED=1; }
FAILED=0

COMPOSE=(docker compose -f docker-compose.yml -f reload/docker-compose.reload.yml)
CONFIG=reload/work/config.yaml
PG_LIVE="${COMPOSE[*]} exec -T client env PGPASSWORD=apppass PGSSLMODE=disable psql -h hoop-inspect -p 15434 -U appuser -d appdb"

# stats reads the admin endpoint over the compose network: a laptop may have
# something else on the published port 19000, and the client container
# reaches the sidecar by name.
stats() { "${COMPOSE[@]}" exec -T client curl -s http://hoop-inspect:19000/stats; }

if [[ ! -f "$CONFIG" ]] || ! stats >/dev/null 2>&1; then
    echo "the reload overlay is not up. Run ./reload/run.sh first." >&2
    exit 1
fi

lanes() { stats | python3 -c '
import json, sys
for l in json.load(sys.stdin)["listeners"]:
    print("  %-12s %-16s active=%d total=%d" % (l["name"], l["addr"], l["active"], l["total"]))'; }

lane_count() { stats | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["listeners"]))'; }

# reload sends SIGHUP and waits for the log to carry the outcome line for
# this edit, so the checks after it read the state the edit produced.
#
# It first waits for the container to see the edit: a bind mount on macOS
# (Docker Desktop, OrbStack) propagates an append with a short delay, and a
# SIGHUP that lands before it re-reads the old bytes. By the ten-second
# poll the bytes are there.
reload() {
    local before
    for _ in $(seq 1 100); do
        "${COMPOSE[@]}" exec -T hoop-inspect cat /etc/hoop-inspect/live/config.yaml 2>/dev/null | cmp -s - "$CONFIG" && break
        sleep 0.2
    done
    before=$("${COMPOSE[@]}" logs --no-log-prefix hoop-inspect 2>/dev/null | grep -c -E 'configuration applied|restart to apply it|keeping the running')
    "${COMPOSE[@]}" kill -s HUP hoop-inspect >/dev/null 2>&1
    for _ in $(seq 1 50); do
        local now
        now=$("${COMPOSE[@]}" logs --no-log-prefix hoop-inspect 2>/dev/null | grep -c -E 'configuration applied|restart to apply it|keeping the running')
        [[ "$now" -gt "$before" ]] && return 0
        sleep 0.2
    done
    fail "the sidecar logged no outcome for the edit within 10s"
}

last_outcome() {
    "${COMPOSE[@]}" logs --no-log-prefix hoop-inspect 2>/dev/null \
      | grep -E 'configuration applied|restart to apply it|keeping the running|listener started|listener stopped' \
      | tail -n "${1:-1}" | sed 's/^/  /'
}

# ------------------------------------------------------------------ before
h "BEFORE / three lanes from sidecar/config.yaml"
lanes
START=$(lane_count)
[[ "$START" == 3 ]] && ok "3 lanes running" || fail "expected 3 lanes, /stats lists $START"

# ------------------------------------------------------ a session that stays
h "SESSION / a psql session on appdb stays open across every edit below"
note "pg_sleep(20) through Envoy. If any edit touched the appdb lane, this"
note "session would die and the row at the end would be missing."
"${COMPOSE[@]}" exec -T client env PGPASSWORD=apppass PGSSLMODE=disable \
    psql -h envoy -p 5432 -U appuser -d appdb -At -c 'select pg_sleep(20), 42' \
    > reload/work/session.out 2>&1 &
SESSION=$!
sleep 1
ok "session open (pid $SESSION)"

# ---------------------------------------------------------------- add a lane
h "ADD / a fourth lane, appdb-live on :15434, appended to the config"
cat >> "$CONFIG" <<'EOF'

  # Added by reload/demo.sh: a second door to appdb that a reload starts.
  - name: appdb-live
    protocol: postgres
    listen: 0.0.0.0:15434
    upstream: appdb:5432
    upstream_tls:
      ca_file: /etc/hoop-inspect/certs/appdb.crt
      server_name: appdb
EOF
reload
last_outcome 2
lanes
[[ "$(lane_count)" == 4 ]] && ok "/stats lists the new lane" || fail "/stats does not list appdb-live"

h "USE / a query through the new lane, past Envoy, masked by the inherited rule"
note "The lane inherited the process's mask rule: email comes back redacted."
OUT=$($PG_LIVE -c 'SELECT name, email FROM customers LIMIT 1;' 2>&1)
echo "$OUT" | sed 's/^/  /'
echo "$OUT" | grep -q 'REDACTED:EMAIL_ADDRESS' && ok "the new lane enforces and masks" || fail "no masked email through appdb-live"

# -------------------------------------------------------------- bind conflict
h "REFUSED / a lane on :19000, the admin port, cannot bind"
note "The sidecar refuses the whole document: the lane does not start, the"
note "running lanes keep their rules, and the reason names the listener and the error."
cp "$CONFIG" reload/work/config.good.yaml
cat >> "$CONFIG" <<'EOF'
  - name: cannot-bind
    protocol: postgres
    listen: 0.0.0.0:19000
    upstream: appdb:5432
EOF
reload
last_outcome 1
[[ "$(lane_count)" == 4 ]] && ok "still 4 lanes; nothing changed" || fail "lane count moved on a refused document"
"${COMPOSE[@]}" logs --no-log-prefix hoop-inspect 2>/dev/null | tail -n 10 | grep -q 'address already in use' \
    && ok "the reason names the bind error" || fail "no bind error in the log"
cp reload/work/config.good.yaml "$CONFIG"
reload >/dev/null
note "  (config restored: a new document that keeps every lane, applied as one generation)"

# ------------------------------------------------------------- remove a lane
h "REMOVE / appdb-live dropped from the config"
python3 - "$CONFIG" <<'EOF'
import sys, re
p = sys.argv[1]
s = open(p).read()
s = re.sub(r"\n  # Added by reload/demo.sh.*?server_name: appdb\n", "\n", s, flags=re.S)
open(p, "w").write(s)
EOF
reload
last_outcome 2
lanes
[[ "$(lane_count)" == 3 ]] && ok "back to 3 lanes" || fail "/stats still lists appdb-live"
if $PG_LIVE -c 'SELECT 1' >/dev/null 2>&1; then
    fail ":15434 still accepts"
else
    ok ":15434 refuses connections"
fi

# ------------------------------------------------------------- the session
h "SESSION / the appdb session from the start finishes normally"
wait "$SESSION"
cat reload/work/session.out | sed 's/^/  /'
grep -q '|42$' reload/work/session.out && ok "the session survived three edits" || fail "the session on appdb was cut"

hr
if [[ "$FAILED" == 0 ]]; then
    printf '\033[32mall checks passed\033[0m\n'
else
    printf '\033[31msome checks failed\033[0m\n'
    exit 1
fi
