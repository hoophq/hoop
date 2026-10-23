#!/usr/bin/env bash
#
# ENG-860 c9. Creates the OpenAI side of the sidecar shape with curl and jq
# only: an environment template that reaches only the sidecar host, a vault
# with the caller token, a saved agent. Then it starts one session that reads
# the users and tries one DELETE, and prints the agent's commands and output.
#
# Usage:
#   SIDECAR_HOST=<host> CALLER_TOKEN_FILE=<file> ./setup.sh
#   ./setup.sh --delete      delete the session, agent, vault and template
#
# OPENAI_API_KEY must hold a key with Agents API access.

set -euo pipefail
cd "$(dirname "$0")"
mkdir -p .work
IDS=.work/setup-ids
: "${OPENAI_API_KEY:?set OPENAI_API_KEY}"

# api <method> <path> [curl args]: the key goes through a file descriptor, not argv.
api() {
    local m=$1 p=$2
    shift 2
    curl -sS --fail-with-body -X "$m" "https://api.openai.com/v1$p" \
        -H @<(printf 'Authorization: Bearer %s\n' "$OPENAI_API_KEY") \
        -H 'OpenAI-Beta: agents=v1' -H 'Content-Type: application/json' "$@"
}

if [[ "${1:-}" == "--delete" ]]; then
    [[ -f $IDS ]] || exit 0
    . $IDS
    # A session still running answers 409 for a while.
    for _ in $(seq 10); do
        api DELETE "/agents/sessions/$SESSION_ID" >/dev/null 2>&1 && break
        sleep 5
    done
    api DELETE "/agents/$AGENT_ID" >/dev/null
    api DELETE "/vaults/$VAULT_ID" >/dev/null
    api DELETE "/agents/environments/templates/$TEMPLATE_ID" >/dev/null
    rm $IDS
    echo "deleted the session, agent, vault and template"
    exit 0
fi

: "${SIDECAR_HOST:?set SIDECAR_HOST}" "${CALLER_TOKEN_FILE:?set CALLER_TOKEN_FILE}"
# A second run would lose the ids of the first, and its vault holds the token.
[[ ! -s $IDS ]] || { echo "a previous run is still up: ./setup.sh --delete first" >&2; exit 1; }

TEMPLATE_ID=$(api POST /agents/environments/templates -d "$(jq -n --arg h "$SIDECAR_HOST" \
    '{name: "hoop sidecar", network: {access: "restricted", allowed_domains: [$h]}}')" | jq -r .id)
VAULT_ID=$(api POST /vaults -d '{"name": "hoop sidecar"}' | jq -r .id)
# --rawfile keeps the token out of argv; the API refuses a trailing newline.
jq -n --arg h "$SIDECAR_HOST" --rawfile v "$CALLER_TOKEN_FILE" \
    '{name: "hoop caller token", auth: {type: "environment_variable", secret_name: "HOOP_CALLER_TOKEN",
      secret_value: ($v | rtrimstr("\n")), networking: {type: "limited", allowed_hosts: [$h]}}}' \
    | api POST "/vaults/$VAULT_ID/credentials" --data-binary @- >/dev/null
AGENT_ID=$(api POST /agents -d "$(jq -n --arg h "$SIDECAR_HOST" '{
    name: "customer-api", model: "gpt-6-sol", reasoning: {effort: "low"},
    instructions: ("You answer questions from the customer REST API at https://\($h). That host is a hoop sidecar: "
      + "it checks policy, masks personal data and records every request. No other host is reachable. "
      + "Send these headers on every request, written exactly so: X-Hoop-Token: $HOOP_CALLER_TOKEN and "
      + "X-OpenAI-Session-Id: <your session id>. A 403 with X-Hoop-Denied is the sidecar policy: report it, do not retry it.")}')" \
    | jq -r .id)
SESSION_ID=$(api POST /agents/sessions -d "$(jq -n --arg t "$TEMPLATE_ID" --arg a "$AGENT_ID" --arg v "$VAULT_ID" \
    '{agent_id: $a, environment: {type: "openai_hosted", environment_template_id: $t, container_size: "small"},
      vault_ids: [$v], metadata: {source: "eng-860 setup.sh"}}')" | jq -r .id)
printf 'TEMPLATE_ID=%s\nVAULT_ID=%s\nAGENT_ID=%s\nSESSION_ID=%s\n' "$TEMPLATE_ID" "$VAULT_ID" "$AGENT_ID" "$SESSION_ID" | tee $IDS

# The sandbox has no variable that holds the session id, so the first message gives it.
PROMPT="Your session id is $SESSION_ID. 1) GET /users and list each user's name, email, phone and card exactly as returned. \
2) Send DELETE /users/1 and report the status and body. The policy is expected to refuse it; do not retry."
api POST "/agents/sessions/$SESSION_ID/events" -d "$(jq -n --arg t "$PROMPT" \
    '{events: [{type: "agent.session.input.message", input: [{role: "user", content: [{type: "input_text", text: $t}]}]}]}')" >/dev/null

echo "waiting for the agent..."
for _ in $(seq 120); do
    st=$(api GET "/agents/sessions/$SESSION_ID/turns?order=desc&limit=1" | jq -r '.data[0].status // "none"')
    [[ "$st" == completed || "$st" == failed || "$st" == cancelled ]] && break
    sleep 3
done
api GET "/agents/sessions/$SESSION_ID/items?order=asc&limit=100" | jq -r '.data[]
    | if .type == "command_execution" then "\n$ \(.command)\n\(.output // "" | rtrimstr("\n"))"
      elif .type == "message" and .role == "assistant" then "\n[agent] \([.content[]?.text // empty] | join(" "))"
      else empty end'
echo -e "\nturn: $st"
