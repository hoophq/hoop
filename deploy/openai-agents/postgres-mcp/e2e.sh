#!/usr/bin/env bash
#
# The real end to end: an OpenAI Agents API session calls this stack's MCP
# endpoint through the quick tunnel. It creates the OpenAI side with
# ./setup.sh, so it checks those request bodies against the live API too.
# Its ids go to a temporary file, never to ./.openai-ids.
#
# It writes evidence/<time>/. It then deletes the vault, the session, the
# agent and the environment template it made; --keep leaves them.
#
# Prereqs: ./run.sh --tunnel, curl, jq, python3, docker, OPENAI_API_KEY.

set -euo pipefail
cd "$(dirname "$0")"

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in curl jq python3 docker; do command -v "$t" >/dev/null || die "missing required tool: $t"; done
: "${OPENAI_API_KEY:?set OPENAI_API_KEY}"
[[ -n "$(docker compose ps -q cloudflared 2>/dev/null)" ]] || die "no tunnel: run ./run.sh --tunnel first"

KEEP=""; [[ "${1:-}" == "--keep" ]] && KEEP=1
MCP_HOST=$(sed -n 's/^MCP_HOST=//p' .env)
TOKEN=$(sed -n 's/^DBHUB_AUTH_TOKEN=//p' .env)
RUN=$(date +%s)
OUT="evidence/$(date -u +%Y%m%dT%H%M%SZ)"

# A quick tunnel can expire while its container runs. This machine is not in
# OpenAI's list, so a live tunnel answers with Caddy's 403.
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 -X POST "https://$MCP_HOST/mcp") || true
[[ "$code" == 403 ]] || die "https://$MCP_HOST/mcp gave '${code:-nothing}', not Caddy's 403: run ./run.sh --tunnel again"
mkdir -p "$OUT"
# After the check above: its log row holds this machine's address.
sleep 1
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# ------------------------------------------------------------------ create
WORK=$(mktemp -d)
export OPENAI_IDS="$WORK/ids" MCP_HOST
cleanup() {
    if [[ -n "$KEEP" ]]; then
        # The vault holds the live token: keep the ids that delete it.
        if [[ -s "$OPENAI_IDS" ]]; then
            cp "$OPENAI_IDS" "$OUT/openai-ids"
            echo "kept: OPENAI_IDS=$OUT/openai-ids ./setup.sh delete removes them"
        fi
        rm -rf "$WORK"
        return
    fi
    # A vault left behind holds the live token, so a failed delete fails the run.
    ./setup.sh delete || { echo "cleanup failed: OPENAI_IDS=$OPENAI_IDS ./setup.sh delete" >&2; return 1; }
    rm -rf "$WORK"
}
on_exit() { local rc=$?; cleanup || rc=1; exit "$rc"; }
trap on_exit EXIT

./setup.sh create

# oai <path>: one GET; the key goes through a file descriptor, not argv.
oai() {
    curl -sS --fail-with-body -H @<(printf 'Authorization: Bearer %s\n' "$OPENAI_API_KEY") \
        -H 'OpenAI-Beta: agents=v1' "https://api.openai.com/v1$1"
}

# ----------------------------------------------------------------- session
PROMPT="This checks the database policy on a fixture table. Run each statement \
below with execute_sql, exactly as written, one call each, in order, and report \
each result as returned. The policy is expected to refuse the DELETE; do not retry it.

1. /* e2e-$RUN-1 */ SELECT id, name, email FROM customers ORDER BY id
2. /* e2e-$RUN-2 */ DELETE FROM customers WHERE id = 1
3. /* e2e-$RUN-3 */ SELECT count(*) AS n FROM customers"

# A turn that does not complete is a result to report, not a reason to stop.
./setup.sh session "$PROMPT" | tee "$OUT/session.txt" || true
SID=$(awk '$1 == "session" {print $2}' "$OPENAI_IDS")
[[ -n "$SID" ]] || die "setup.sh session recorded no session"

# Items can arrive after their turn completes (ENG-860 K3), so wait for the
# last tool call to be followed by a message.
items() {
    local after="" page all="[]"
    while :; do
        page=$(oai "/agents/sessions/$SID/items?order=asc&limit=100${after:+&after=$after}")
        all=$(jq -c --argjson a "$all" '$a + .data' <<<"$page")
        [[ "$(jq -r .has_more <<<"$page")" == true ]] || break
        after=$(jq -r .last_id <<<"$page")
    done
    printf '%s\n' "$all"
}
for _ in $(seq 20); do
    items > "$OUT/items.json"
    [[ "$(jq -r '[.[] | select(.type == "mcp_call" or .type == "message")] | last | .type // ""' "$OUT/items.json")" == message ]] && break
    sleep 3
done
oai "/agents/sessions/$SID" > "$OUT/session.json"
oai "/agents/sessions/$SID/turns?order=asc&limit=100" > "$OUT/turns.json"

# --------------------------------------------------------------- evidence
# output: a string, or the MCP result with its content blocks.
jq -r '
    def text: if type == "string" then . else [.content[]?.text // empty] | join("") end;
    .[] |
    if   .type == "message" and .role == "user"      then "## user\n\n" + ([.content[]?.text // empty] | join("")) + "\n"
    elif .type == "mcp_call"                         then "### tool call: \(.server_label).\(.name) (\(.status))\n\n```sql\n\(.arguments.sql // (.arguments | tostring))\n```\n\n```\n\(.output | text)\(if .error then "\nerror: \(.error | tostring)" else "" end)\n```\n"
    elif .type == "message" and .role == "assistant" then "## agent\n\n" + ([.content[]?.text // empty] | join("")) + "\n"
    else empty end' "$OUT/items.json" > "$OUT/transcript.md"
{ docker compose logs --no-log-prefix --since "$SINCE" sidecar | grep '^{' || true; } \
    | jq -c "select(.statement // \"\" | contains(\"e2e-$RUN-\"))" > "$OUT/audit.jsonl"
{ docker compose logs --no-log-prefix --since "$SINCE" caddy | grep '^{' || true; } \
    | jq -c 'select(.request.uri? == "/mcp") | {ts, method: .request.method, status, ua: .request.headers["User-Agent"][0],
             client_ip: (if .status == 403 then "refused" else .request.client_ip end)}' \
    > "$OUT/caddy.jsonl"
docker compose logs --no-log-prefix --since "$SINCE" postgres | grep "e2e-$RUN-" > "$OUT/postgres.log" || true

# ------------------------------------------------------------------ checks
count() { wc -l | tr -d ' '; }
expect() { # expect <got> <want> <description>
    if [[ "$1" == "$2" ]]; then echo "PASS $3"; else echo "FAIL $3 (expected '$2', got '$1')"; fi
}
# The tool call whose SQL holds tag <n>.
call_of() { jq -c --arg t "e2e-$RUN-$1 " '[.[] | select(.type == "mcp_call" and (.arguments.sql // "" | contains($t)))] | first // empty' "$OUT/items.json"; }
text_of() { jq -r '(.output | if type == "string" then . else [.content[]?.text // empty] | join("") end) + (.error // "" | tostring)'; }
row() { jq -c --arg t "e2e-$RUN-$1 " 'select(.direction == "client" and (.statement | contains($t)))' "$OUT/audit.jsonl"; }
listed() { grep '^@outside not client_ip ' ranges/openai.caddy | cut -d' ' -f4- | tr ' ' '\n' | grep .; }
# Caller addresses of forwarded calls that no listed prefix holds; a missing
# address counts as outside, so a renamed log field fails here. A refused
# caller's address is not kept: it is not OpenAI's, and may be a person's.
unlisted() {
    python3 -I - <(listed) <(jq -r 'select(.status == 200) | .client_ip // "missing"' "$OUT/caddy.jsonl") <<'EOF'
import ipaddress, sys
nets = [ipaddress.ip_network(t) for t in open(sys.argv[1]).read().split()]
for a in open(sys.argv[2]).read().split():
    try:
        if any(ipaddress.ip_address(a) in n for n in nets):
            continue
    except ValueError:
        pass
    print(a)
EOF
}

{
    expect "$(jq -r '.data | last | .status' "$OUT/turns.json")" completed "the turn completed"
    expect "$(jq -r '[.[] | select(.type == "mcp_call" or .type == "message")] | last | .type' "$OUT/items.json")" message \
        "the agent answered after its last tool call"
    expect "$(jq -r '.environment.network.access' "$OUT/session.json")" disabled "the sandbox has no network"
    expect "$(jq '[.[] | select(.type == "mcp_call")] | length' "$OUT/items.json")" 3 "3 MCP calls"
    expect "$(jq -r '[.[] | select(.type == "mcp_call") | "\(.server_label).\(.name)"] | unique | join(",")' "$OUT/items.json")" \
        postgres.execute_sql "every call is postgres.execute_sql"
    expect "$(call_of 1 | text_of | grep -o 'REDACTED:EMAIL_ADDRESS' | count)" 3 \
        "the SELECT comes back with 3 emails masked"
    expect "$(cat "$OUT"/items.json "$OUT"/session.txt | grep -cE 'ada@example.com|grace@example.org|alan@example.net')" 0 \
        "no stored email reaches the session"
    expect "$(call_of 2 | jq -r '.status')" failed "the DELETE is a failed tool call"
    expect "$(call_of 2 | text_of | grep -c 'writes are not permitted')" 1 "the error is the sidecar's message"
    expect "$(call_of 3 | text_of | jq -r '.data.statements[0].rows[0].n')" 3 "the count after it is 3"
    expect "$(cat "$OUT"/*.json "$OUT"/session.txt | grep -cF "$TOKEN")" 0 "dbhub's token is in no session record"
    for n in 1 2 3; do
        expect "$(row "$n" | count)" 1 "statement $n has one client audit row"
    done
    expect "$(row 2 | jq -r '[.kind, .allowed, .rule] | join(" ")')" "violation false read-only" \
        "the DELETE row is a violation by rule read-only"
    expect "$(grep -cE "e2e-$RUN-(1|3) " "$OUT/postgres.log")" 2 "Postgres ran statements 1 and 3"
    expect "$(grep -c "e2e-$RUN-2 " "$OUT/postgres.log")" 0 "the DELETE never reached Postgres"
    expect "$(jq -c 'select(.status == 200)' "$OUT/caddy.jsonl" | count | sed 's/^[1-9][0-9]*$/yes/')" yes \
        "Caddy forwarded /mcp calls"
    expect "$(unlisted | count)" 0 "every forwarded /mcp call came from OpenAI's connectors list"
} | tee "$OUT/result.txt"

echo "evidence: $OUT"
! grep -q '^FAIL' "$OUT/result.txt"
