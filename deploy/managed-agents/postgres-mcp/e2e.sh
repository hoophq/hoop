#!/usr/bin/env bash
#
# The real end to end: a Managed Agents cloud session calls this stack's MCP
# endpoint through the quick tunnel. It applies this template's ant and
# credential files, so it checks them against the live API too. The tunnel
# host replaces mcp.example.com in a temporary copy.
#
# It writes evidence/<time>/. It then deletes the vault and the environment
# it made and archives the agent and the session; --keep leaves them.
#
# Prereqs: ./run.sh --tunnel, ant 1.42.0 or later, jq, ANTHROPIC_API_KEY.
# MODEL=claude-opus-5-5 ./e2e.sh overrides the agent file's model.

set -euo pipefail
cd "$(dirname "$0")"

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in ant jq docker; do command -v "$t" >/dev/null || die "missing required tool: $t"; done
[[ -n "${ANTHROPIC_API_KEY:-}" ]] || die "set ANTHROPIC_API_KEY"
[[ -n "$(docker compose ps -q cloudflared 2>/dev/null)" ]] || die "no tunnel: run ./run.sh --tunnel first"

KEEP=""; [[ "${1:-}" == "--keep" ]] && KEEP=1
MCP_HOST=$(sed -n 's/^MCP_HOST=//p' .env)
TOKEN=$(sed -n 's/^DBHUB_AUTH_TOKEN=//p' .env)
RUN=$(date +%s)
OUT="evidence/$(date -u +%Y%m%dT%H%M%SZ)"
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
mkdir -p "$OUT"

# ------------------------------------------------------------------ create
# Only the files this script creates and removes: never a committed
# claude-lock.json, or apply would update your real resources and the
# cleanup would delete them.
AGENT=agents/postgres-analyst.md ENVIRONMENT=environments/hoop-postgres-mcp.yaml VAULT=vaults/hoop-postgres-mcp.yaml
WORK=$(mktemp -d)
mkdir -p "$WORK/ant/agents" "$WORK/ant/environments" "$WORK/ant/vaults"
for f in "$AGENT" "$ENVIRONMENT" "$VAULT"; do cp "ant/$f" "$WORK/ant/$f"; done
cp credentials/dbhub-token.yaml "$WORK/"
sed -i.bak "s/mcp\.example\.com/$MCP_HOST/g" "$WORK/ant/$AGENT" "$WORK/dbhub-token.yaml"
[[ -z "${MODEL:-}" ]] || sed -i.bak "s/^model: .*/model: $MODEL/" "$WORK/ant/$AGENT"

AGENT_ID="" VAULT_ID="" ENV_ID="" SESSION_ID="" FAILED_CLEANUP=""
drop() { # drop <label> <id> <ant args, ending with the id flag>
    local label=$1 id=$2; shift 2
    [[ -z "$id" ]] || ant "$@" "$id" >/dev/null || FAILED_CLEANUP+=" $label"
}
cleanup() {
    rm -rf "$WORK"
    [[ -z "$KEEP" ]] || { echo "kept: agent $AGENT_ID, vault $VAULT_ID, environment $ENV_ID"; return; }
    # The vault holds the token, so it goes first.
    drop vault "$VAULT_ID" beta:vaults delete --vault-id
    drop session "$SESSION_ID" beta:sessions archive --session-id
    drop agent "$AGENT_ID" beta:agents archive --agent-id
    drop environment "$ENV_ID" beta:environments delete --environment-id
    if [[ -n "$FAILED_CLEANUP" ]]; then echo "cleanup failed for:$FAILED_CLEANUP" >&2
    else echo "cleaned up the vault, session, agent and environment"; fi
}
trap cleanup EXIT

(cd "$WORK/ant" && ant apply --yes . >/dev/null)
lockid() {
    local id
    id=$(jq -r --arg f "./$1" '.resources[$f].id // empty' "$WORK/ant/claude-lock.json")
    [[ -n "$id" ]] || die "ant apply recorded no id for $1"
    echo "$id"
}
AGENT_ID=$(lockid "$AGENT")
ENV_ID=$(lockid "$ENVIRONMENT")
VAULT_ID=$(lockid "$VAULT")
ant beta:vaults:credentials create --vault-id "$VAULT_ID" \
    --auth.token @<(printf %s "$TOKEN") < "$WORK/dbhub-token.yaml" >/dev/null
echo "agent $AGENT_ID, environment $ENV_ID, vault $VAULT_ID"

# ----------------------------------------------------------------- session
PROMPT="This checks the database policy on a fixture table. Run each statement \
below with execute_sql, exactly as written, one call each, in order, and report \
each result as returned. The policy is expected to refuse the DELETE; do not retry it.

1. /* e2e-$RUN-1 */ SELECT id, name, email FROM customers ORDER BY id
2. /* e2e-$RUN-2 */ DELETE FROM customers WHERE id = 1
3. /* e2e-$RUN-3 */ SELECT count(*) AS n FROM customers"

SESSION_ID=$(ant beta:sessions create --agent "$AGENT_ID" --environment-id "$ENV_ID" \
    --vault-id "$VAULT_ID" --title "hoop postgres-mcp e2e $RUN" \
    --budget '{type: limit, max_list_cost: {amount: "100", currency: USD}}' \
    --initial-event "$(jq -nc --arg t "$PROMPT" '{type: "user.message", content: [{type: "text", text: $t}]}')" \
    --transform id --raw-output)
echo "session $SESSION_ID"

events() { ant beta:sessions:events list --session-id "$SESSION_ID" --max-items -1 --format jsonl; }
FINISHED=""
for _ in $(seq 120); do
    sleep 5
    last=$(events | jq -c 'select(.type | startswith("session."))' | tail -1)
    case "$(jq -r '.type' <<<"$last")" in
    session.status_idle)
        [[ "$(jq -r '.stop_reason.type // ""' <<<"$last")" == requires_action ]] || { FINISHED=1; break; } ;;
    session.error|session.status_terminated) FINISHED=1; break ;;
    esac
done
events > "$OUT/events.jsonl"

# --------------------------------------------------------------- evidence
# `|| true`: no match is a result to report, not a reason to stop here.
jq -r '
    if   .type == "user.message"           then "## user\n\n" + ([.content[].text] | join("")) + "\n"
    elif .type == "agent.mcp_tool_use"     then "### tool call: \(.name)\n\n```sql\n\(.input.sql // (.input | tostring))\n```\n"
    elif .type == "agent.mcp_tool_result"  then "### tool result\(if .is_error then " (error)" else "" end)\n\n```\n" + ([.content[]?.text] | join("")) + "\n```\n"
    elif .type == "agent.message"          then "## agent\n\n" + ([.content[]?.text] | join("")) + "\n"
    elif .type == "session.error"          then "## session.error\n\n\(. | tostring)\n"
    else empty end' "$OUT/events.jsonl" > "$OUT/transcript.md"
{ docker compose logs --no-log-prefix --since "$SINCE" sidecar | grep '^{' || true; } \
    | jq -c "select(.statement // \"\" | contains(\"e2e-$RUN-\"))" > "$OUT/audit.jsonl"
{ docker compose logs --no-log-prefix --since "$SINCE" caddy | grep '^{' || true; } \
    | jq -c 'select(.request.uri? == "/mcp") | {ts, client_ip: .request.client_ip, method: .request.method, status, ua: .request.headers["User-Agent"][0]}' \
    > "$OUT/caddy.jsonl"
docker compose logs --no-log-prefix --since "$SINCE" postgres | grep "e2e-$RUN-" > "$OUT/postgres.log" || true

# ------------------------------------------------------------------ checks
count() { wc -l | tr -d ' '; }
expect() { # expect <got> <want> <description>
    if [[ "$1" == "$2" ]]; then echo "PASS $3"; else echo "FAIL $3 (expected '$2', got '$1')"; fi
}
of_type() { jq -c --arg t "$1" 'select(.type == $t)' "$OUT/events.jsonl"; }
# The tool result of the call whose SQL holds tag <n>, matched by its id.
result_of() {
    local id
    id=$(of_type agent.mcp_tool_use | jq -r --arg t "e2e-$RUN-$1 " 'select(.input.sql // "" | contains($t)) | .id' | head -1)
    of_type agent.mcp_tool_result | jq -c --arg id "$id" 'select(.mcp_tool_use_id == $id)'
}
text_of() { jq -r '[.content[]?.text] | join("")'; }
row() { jq -c --arg t "e2e-$RUN-$1 " 'select(.direction == "client" and (.statement | contains($t)))' "$OUT/audit.jsonl"; }
in_range='^160\.79\.(10[4-9]|11[01])\.'

{
    expect "$FINISHED" 1 "the session finished within 10 minutes"
    expect "$(of_type session.error | count)" 0 "no session error"
    expect "$(of_type agent.mcp_tool_use | count)" 3 "3 execute_sql calls"
    expect "$(result_of 1 | text_of | grep -o 'REDACTED:EMAIL_ADDRESS' | count)" 3 \
        "the SELECT comes back with 3 emails masked"
    expect "$(grep -cE 'ada@example.com|grace@example.org|alan@example.net' "$OUT/events.jsonl")" 0 \
        "no stored email reaches the session"
    expect "$(result_of 2 | jq -r '.is_error')" true "the DELETE is a tool error"
    expect "$(result_of 2 | text_of | grep -c 'writes are not permitted')" 1 "the error is the sidecar's message"
    expect "$(result_of 3 | text_of | jq -r '.data.statements[0].rows[0].n')" 3 "the count after it is 3"
    for n in 1 2 3; do
        expect "$(row "$n" | count)" 1 "statement $n has one client audit row"
    done
    expect "$(row 2 | jq -r '[.kind, .allowed, .rule] | join(" ")')" "violation false read-only" \
        "the DELETE row is a violation by rule read-only"
    expect "$(grep -cE "e2e-$RUN-(1|3) " "$OUT/postgres.log")" 2 "Postgres ran statements 1 and 3"
    expect "$(grep -c "e2e-$RUN-2 " "$OUT/postgres.log")" 0 "the DELETE never reached Postgres"
    expect "$(jq -c 'select(.status == 200)' "$OUT/caddy.jsonl" | count | sed 's/^[1-9][0-9]*$/yes/')" yes \
        "Caddy forwarded /mcp calls"
    # A missing client_ip counts as outside, so a renamed log field fails here.
    expect "$(jq -c --arg r "$in_range" 'select(.status == 200 and (.client_ip // "" | test($r) | not))' "$OUT/caddy.jsonl" | count)" 0 \
        "every forwarded /mcp call came from 160.79.104.0/21"
} | tee "$OUT/result.txt"

echo "evidence: $OUT"
! grep -q '^FAIL' "$OUT/result.txt"
