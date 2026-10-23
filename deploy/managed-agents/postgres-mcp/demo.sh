#!/usr/bin/env bash
#
# Checks every claim of this template on this machine, with no Anthropic
# call. The probe container stands where cloudflared stands, so it can send
# any caller address in Cf-Connecting-Ip.
#
#   1. topology  dbhub reaches the sidecar and nothing else
#   2. edge      only Anthropic's range, only /mcp, only with the token
#   3. MCP       a SELECT comes back masked; the sidecar refuses writes
#   4. audit     one row per statement; a refused one never reaches Postgres
#
# Prereqs: ./run.sh --test (or --tunnel), jq.

# No -e: a failed check records the failure, and the next checks still run.
set -uo pipefail
cd "$(dirname "$0")" || exit 1

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
pass() { printf '\033[32m  PASS\033[0m %s\n' "$*"; }
fail() { printf '\033[31m  FAIL\033[0m %s\n' "$*"; FAILED=1; }
FAILED=0

# `[[ ... ]] && pass || fail` also runs fail when pass returns nonzero.
expect() { # expect <got> <want> <description>
    if [[ "$1" == "$2" ]]; then pass "$3"; else fail "$3 (expected '$2', got '$1')"; fi
}
expect_not() { # expect_not <got> <unwanted> <description>
    if [[ "$1" != "$2" ]]; then pass "$3 ($1)"; else fail "$3 (got '$1')"; fi
}
contains() { # contains <text> <substring> <description>
    if [[ "$1" == *"$2"* ]]; then pass "$3"; else fail "$3 (no '$2' in: ${1:0:120})"; fi
}
count() { wc -l | tr -d ' '; }

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
[[ -n "$(docker compose ps -q probe 2>/dev/null)" ]] \
    || { echo "The test overlay is not up. Run ./run.sh --test first." >&2; exit 1; }

TOKEN=$(sed -n 's/^DBHUB_AUTH_TOKEN=//p' .env)
ANTHROPIC_IP=160.79.104.5
RUN=$(date +%s)
SEEDED='ada@example.com|grace@example.org|alan@example.net'
REFUSAL="writes are not permitted"
DIRECT="docker compose exec -T postgres psql -U owner -d appdb -tA -c"
audit() { docker compose logs --no-log-prefix sidecar | grep '^{'; }

# no_route <host> <port> <description>: connect from inside dbhub, and pass
# only on a known refusal. A failed exec prints nothing, which must not pass.
no_route() {
    local got
    got=$(docker compose exec -T dbhub node -e '
        const s = require("net").connect({host: process.argv[1], port: +process.argv[2]});
        s.setTimeout(3000, () => { console.log("timeout"); process.exit(0); });
        s.on("connect", () => { console.log("ok"); process.exit(0); });
        s.on("error", e => { console.log(e.code || e.message); process.exit(0); });' "$1" "$2")
    case "$got" in
    timeout|ENOTFOUND|EAI_AGAIN|EHOSTUNREACH|ENETUNREACH|ECONNREFUSED) pass "$3 ($got)" ;;
    *) fail "$3 (got '$got')" ;;
    esac
}

# post <caller ip|-> <token|-> <path> [json]: a request to Caddy as the tunnel
# would send it. Prints the body, then the HTTP code on the last line.
post() {
    local args=(-s -w '\n%{http_code}' -H 'Content-Type: application/json'
                -H 'Accept: application/json, text/event-stream')
    [[ "$1" != - ]] && args+=(-H "Cf-Connecting-Ip: $1")
    [[ "$2" != - ]] && args+=(-H "Authorization: Bearer $2")
    docker compose exec -T probe curl "${args[@]}" -d "${4:-{\}}" "http://caddy:8080$3"
}
code_of() { post "$@" | tail -n1; }
LIST='{"jsonrpc":"2.0","id":1,"method":"tools/list"}'

# rpc <json>: from Anthropic's range with the token. Prints the JSON-RPC
# reply from dbhub's one-event stream, or nothing when no answer came.
rpc() { post "$ANTHROPIC_IP" "$TOKEN" /mcp "$1" | sed '$d' | sed -n 's/^data: //p'; }
sql() {
    rpc "$(jq -nc --arg s "$1" \
        '{jsonrpc:"2.0",id:1,method:"tools/call",params:{name:"execute_sql",arguments:{sql:$s}}}')"
}
is_error() { jq -r '.result.isError // false'; }
text_of()  { jq -r '.result.content[0].text'; }

# ------------------------------------------------------------------ topology
h "1. TOPOLOGY / dbhub reaches the sidecar and nothing else"
CONFIG=$(docker compose config --format json)
for svc in postgres sidecar dbhub; do
    expect "$(jq -r --arg s "$svc" '.services[$s].ports // [] | length' <<<"$CONFIG")" 0 "$svc publishes no port"
done
for net in edge pgwire data; do
    expect "$(docker network inspect "hoop-postgres-mcp_$net" -f '{{.Internal}}')" true "network $net is internal"
done
PG_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$(docker compose ps -q postgres)")
no_route postgres 5432 "dbhub cannot resolve postgres"
no_route "$PG_IP" 5432 "dbhub cannot reach postgres by IP $PG_IP"
no_route sidecar 19000 "dbhub cannot reach the sidecar admin API"
no_route 1.1.1.1 443   "dbhub has no route to the internet"
expect "$(docker compose exec -T dbhub node -e '
    require("net").connect({host: "sidecar", port: 15432}).on("connect", () => { console.log("ok"); process.exit(0); })
    .on("error", e => { console.log(e.code); process.exit(0); })')" ok "dbhub reaches the sidecar lane"

# ---------------------------------------------------------------------- edge
h "2. EDGE / only Anthropic's range, only /mcp, only with the token"
expect "$(code_of "$ANTHROPIC_IP" "$TOKEN" /mcp "$LIST")" 200 "$ANTHROPIC_IP, inside the range"
expect "$(code_of 160.79.111.255  "$TOKEN" /mcp "$LIST")" 200 "160.79.111.255, last address of the range"
for ip in 160.79.103.255 160.79.112.0 203.0.113.9; do
    expect "$(code_of "$ip" "$TOKEN" /mcp "$LIST")" 403 "$ip, outside the range"
done
expect "$(code_of - "$TOKEN" /mcp "$LIST")" 403 "no caller header: the probe's own address is outside"
# dbhub is on the edge network but is not a trusted proxy, so Caddy ignores
# the header it sends and judges its real address.
spoof=$(docker compose exec -T dbhub node -e '
    fetch("http://caddy:8080/mcp", {method: "POST", body: process.argv[3], headers: {
        "Cf-Connecting-Ip": process.argv[2], "Authorization": "Bearer " + process.argv[1],
        "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}})
    .then(r => console.log(r.status), e => console.log(e.message))' "$TOKEN" "$ANTHROPIC_IP" "$LIST")
expect "$spoof" 403 "a caller header from an untrusted peer is ignored"
expect "$(code_of "$ANTHROPIC_IP" - /mcp "$LIST")" 401 "no token: dbhub refuses"
expect "$(code_of "$ANTHROPIC_IP" wrong-token /mcp "$LIST")" 401 "wrong token: dbhub refuses"
for path in / /api/sources /healthz /mcp/x; do
    expect "$(code_of "$ANTHROPIC_IP" "$TOKEN" "$path")" 404 "$path is not routed"
done
direct=$(docker compose exec -T dbhub node -e '
    fetch("http://127.0.0.1:8080/api/sources", {headers: {"Authorization": "Bearer " + process.argv[1]}})
    .then(r => console.log(r.status), e => console.log(e.message))' "$TOKEN")
expect "$direct" 200 "dbhub does serve /api/sources: the edge is what hides it"
# The production config: the same Caddyfile without the overlay's variables.
prod=$(docker compose exec -T caddy env -u TRUSTED_PROXIES -u CLIENT_IP_HEADER SITE_ADDRESS=mcp.example.com \
    caddy adapt --config /etc/caddy/Caddyfile 2>/dev/null)
expect "$(jq -c '.apps.http.servers[].trusted_proxies.ranges' <<<"$prod")" '["127.0.0.1/32"]' \
    "production config trusts no proxy but loopback"
expect "$(jq -r '[.. | .client_ip? // empty | .ranges[]] | unique | join(",")' <<<"$prod")" 160.79.104.0/21 \
    "production config allows only 160.79.104.0/21"

# ----------------------------------------------------------------------- MCP
h "3. MCP / a SELECT comes back masked; the sidecar refuses writes"
note "Stored values, read as the owner with the sidecar bypassed:"
$DIRECT "SELECT id, name, email FROM customers ORDER BY id"

contains ",$(rpc "$LIST" | jq -r '[.result.tools[].name] | join(",")')," ",execute_sql," "tools/list offers execute_sql"

r=$(sql "/* demo-$RUN-1 */ SELECT id, name, email FROM customers ORDER BY id")
t=$(text_of <<<"$r")
note "$(jq -c '.data.statements[0].rows' <<<"$t" 2>/dev/null || echo "$t")"
expect "$(is_error <<<"$r")" false "SELECT succeeds"
expect "$(grep -o 'REDACTED:EMAIL_ADDRESS' <<<"$t" | count)" 3 "3 emails masked"
expect "$(grep -cE "$SEEDED" <<<"$t")" 0 "no stored email reaches the agent"
contains "$t" "Ada Lovelace" "names pass unmasked"

t=$(sql "/* demo-$RUN-2 */ SELECT email AS contact FROM customers" | text_of)
expect "$(grep -o 'REDACTED:EMAIL_ADDRESS' <<<"$t" | count)" 3 "an alias does not unmask: the rule keys on the value"
expect "$(grep -cE "$SEEDED" <<<"$t")" 0 "no stored email under the alias"

r=$(sql "/* demo-$RUN-3 */ DELETE FROM customers WHERE id = 1")
note "$(text_of <<<"$r" | jq -r '.error' 2>/dev/null)"
expect "$(is_error <<<"$r")" true "DELETE returns a tool error"
contains "$(text_of <<<"$r")" "$REFUSAL" "the agent reads the sidecar's message"
expect "$($DIRECT 'SELECT count(*) FROM customers')" 3 "3 rows remain"

# The refusal closed dbhub's connection. Its pool opens a new one.
r=$(sql "/* demo-$RUN-4 */ SELECT count(*) AS n FROM customers")
expect "$(text_of <<<"$r" | jq -r '.data.statements[0].rows[0].n')" 3 "the next statement works at once"

# The sidecar's message, not only an error: the demo login may only
# SELECT, so Postgres would refuse every write by itself.
i=10
for s in "WITH g AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM g" \
         "MERGE INTO customers c USING (SELECT 1 AS id) s ON c.id = s.id WHEN MATCHED THEN DELETE" \
         "EXPLAIN ANALYZE DELETE FROM customers" \
         "DO \$\$BEGIN DELETE FROM customers; END\$\$" \
         "PREPARE wipe AS DELETE FROM customers" \
         "COPY customers FROM STDIN" \
         "TRUNCATE customers" \
         "CREATE TABLE customers_copy AS SELECT * FROM customers" \
         "SET statement_timeout = 0"; do
    i=$((i + 1))
    contains "$(sql "/* demo-$RUN-$i */ $s" | text_of)" "$REFUSAL" "the sidecar refuses: ${s:0:56}"
done
expect "$($DIRECT 'SELECT count(*) FROM customers')" 3 "3 rows remain"
# What the classifier misses (ENG-803), the login refuses.
expect "$(sql "/* demo-$RUN-20 */ SELECT * INTO TEMP scratch FROM customers" | is_error)" true \
    "the login refuses a temporary table"
# The read-only default answers first, and set_config() can lift it; the
# missing grant still holds.
expect "$($DIRECT "SELECT has_database_privilege('agent', 'appdb', 'TEMPORARY')")" f \
    "the login has no TEMPORARY privilege"
contains "$(sql "/* demo-$RUN-21 */ SELECT lo_from_bytea(0, 'x')" | text_of)" \
    "permission denied" "the login refuses a large object"

note "A refused write inside a multi-statement call (bytebase/dbhub#479):"
dbhub_id=$(docker compose ps -q dbhub)
before=$(docker inspect -f '{{.RestartCount}}' "$dbhub_id")
sql "/* demo-$RUN-30 */ SELECT 1; DELETE FROM customers" >/dev/null
restarted=""
for _ in $(seq 20); do
    [[ "$(docker inspect -f '{{.RestartCount}}' "$dbhub_id")" -gt "$before" ]] && { restarted=1; break; }
    sleep 1
done
if [[ -n "$restarted" ]]; then
    note "  dbhub exited and Docker restarted it, the known gap in README.md."
    healthy=""
    for _ in $(seq 60); do
        [[ "$(docker compose ps dbhub --format '{{.Health}}')" == healthy ]] && { healthy=1; break; }
        sleep 1
    done
    expect "$healthy" 1 "dbhub is healthy again"
else
    fail "dbhub survived: bytebase/dbhub#479 or ENG-800 is fixed. Update the dbhub comment in docker-compose.yml, the README limit and this check."
fi
expect "$($DIRECT 'SELECT count(*) FROM customers')" 3 "3 rows remain"

# --------------------------------------------------------------------- audit
h "4. AUDIT / one row per statement; a refused one never reaches Postgres"
AUDIT=$(audit)
row() { jq -c --arg t "demo-$RUN-$1 " 'select(.direction == "client" and (.statement // "" | contains($t)))' <<<"$AUDIT"; }
for n in 1 2 3 4; do
    expect "$(row "$n" | count)" 1 "statement $n has one client row"
done
note "$(row 3 | jq -c '{kind, operation, allowed, rule, message}')"
expect "$(row 3 | jq -r '[.kind, .allowed, .rule] | join(" ")')" "violation false read-only" \
    "the DELETE row is a violation by rule read-only"
expect_not "$(jq -c 'select(.kind == "masked")' <<<"$AUDIT" | count)" 0 "masked rows are recorded"
expect "$(grep -cE "$SEEDED" <<<"$AUDIT")" 0 "no stored email in the audit trail"
PGLOG=$(docker compose logs --no-log-prefix postgres)
expect "$(grep -c "demo-$RUN-1 " <<<"$PGLOG")" 1 "Postgres ran the SELECT"
reached=""
for n in 3 $(seq 11 19); do
    [[ "$(grep -c "demo-$RUN-$n " <<<"$PGLOG")" == 0 ]] || reached+=" $n"
done
expect "${reached:-none}" none "no refused statement reached Postgres"

# ------------------------------------------------------------------- summary
h "SUMMARY"
if [[ "$FAILED" == 0 ]]; then
    printf '\033[32mall checks passed\033[0m\n'
    note "Next: ./run.sh --tunnel, then ./e2e.sh for a real Managed Agents session."
else
    printf '\033[31msome checks failed\033[0m\n'
fi
exit "$FAILED"
