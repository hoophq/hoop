#!/usr/bin/env bash
#
# Creates the OpenAI side of this template from the request bodies in
# openai/: an environment template with no network, a vault that holds
# dbhub's token for the MCP URL, and the agent. README.md has the steps.
#
# Usage:
#   ./setup.sh create              create the template, vault, credential, agent
#   ./setup.sh create --dry-run    print those request bodies: no secret, no API call
#   ./setup.sh session <question>  start a session, ask <question>, print the turn
#   ./setup.sh delete              delete what create and session made
#
# Reads MCP_HOST and DBHUB_AUTH_TOKEN from the .env that ./run.sh wrote;
# MCP_HOST in the environment wins. OPENAI_API_KEY for all but --dry-run.
# Ids go to OPENAI_IDS, default ./.openai-ids. Needs curl and jq 1.6 or later.

set -euo pipefail
cd "$(dirname "$0")"
ids=${OPENAI_IDS:-.openai-ids}

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in curl jq; do command -v "$t" >/dev/null || die "missing required tool: $t"; done

MCP_HOST=${MCP_HOST:-$(sed -n 's/^MCP_HOST=//p' .env 2>/dev/null || true)}

# oai <curl args>: the key goes through a file descriptor, not argv.
oai() {
    curl -sS -H @<(printf 'Authorization: Bearer %s\n' "$OPENAI_API_KEY") \
        -H 'OpenAI-Beta: agents=v1' -H 'Content-Type: application/json' "$@"
}

# api <method> <path> [curl args]: prints the response, or fails with it.
api() {
    local m=$1 p=$2 out
    shift 2
    out=$(oai --fail-with-body -X "$m" "https://api.openai.com/v1$p" "$@") \
        || { printf '%s %s: %s\n' "$m" "$p" "$out" >&2; return 1; }
    printf '%s\n' "$out"
}

# The ids file holds one "<kind> <id>" line per resource, so a delete that
# fails halfway can be run again.
id() { awk -v k="$1" '$1 == k {print $2}' "$ids" 2>/dev/null || true; }
record() { printf '%s %s\n' "$1" "$2" >> "$ids"; }
forget() { awk -v k="$1" -v v="$2" '!($1 == k && $2 == v)' "$ids" > "$ids.tmp" && mv "$ids.tmp" "$ids"; }

# The committed files name mcp.example.com; each request names MCP_HOST.
body() { jq --arg h "$MCP_HOST" '(.. | strings) |= gsub("mcp\\.example\\.com"; $h)' "openai/$1"; }
agent_body() {
    jq --arg h "$MCP_HOST" --rawfile i openai/agent.md \
        '.instructions = $i | (.. | strings) |= gsub("mcp\\.example\\.com"; $h)' openai/agent.json
}

create() {
    [[ -n "$MCP_HOST" ]] || die "no MCP_HOST: run ./run.sh first, or set MCP_HOST"
    [[ "$MCP_HOST" =~ ^[A-Za-z0-9.-]+$ ]] \
        || die "MCP_HOST must be a bare host name: no scheme, port or path"
    if [[ "${1:-}" == --dry-run ]]; then
        jq -n --argjson t "$(body environment-template.json)" --argjson v "$(body vault.json)" \
            --argjson c "$(body credentials/dbhub-token.json)" --argjson a "$(agent_body)" \
            '{environment_template: $t, vault: $v, credential: $c, agent: $a}'
        return
    fi
    [[ -z "${1:-}" ]] || die "unknown argument: $1"
    [[ "$MCP_HOST" != mcp.invalid ]] || die "MCP_HOST is mcp.invalid (test mode): run ./run.sh or ./run.sh --tunnel"
    : "${OPENAI_API_KEY:?set OPENAI_API_KEY}"
    grep -q '^DBHUB_AUTH_TOKEN=.' .env 2>/dev/null || die "no DBHUB_AUTH_TOKEN in .env: run ./run.sh first"
    [[ ! -s "$ids" ]] || die "$ids already lists resources: run ./setup.sh delete first"

    local template vault agent
    template=$(body environment-template.json | api POST /agents/environments/templates --data-binary @- | jq -er .id)
    record template "$template"
    vault=$(body vault.json | api POST /vaults --data-binary @- | jq -er .id)
    record vault "$vault"
    # --rawfile keeps the token out of argv; the API refuses a trailing newline.
    body credentials/dbhub-token.json \
        | jq --rawfile v <(sed -n 's/^DBHUB_AUTH_TOKEN=//p' .env) '.auth.token = ($v | rtrimstr("\n"))' \
        | api POST "/vaults/$vault/credentials" --data-binary @- >/dev/null
    agent=$(agent_body | api POST /agents --data-binary @- | jq -er .id)
    record agent "$agent"
    echo "created template $template, vault $vault, agent $agent for https://$MCP_HOST/mcp"
}

session() {
    local question=${1:?usage: ./setup.sh session <question>}
    : "${OPENAI_API_KEY:?set OPENAI_API_KEY}"
    local template vault agent sid
    template=$(id template) vault=$(id vault) agent=$(id agent)
    [[ -n "$template" && -n "$vault" && -n "$agent" ]] || die "no resources in $ids: run ./setup.sh create first"

    # A template cannot hold a vault, so each session names it.
    sid=$(jq --arg t "$template" --arg v "$vault" --arg a "$agent" \
        '.agent_id = $a | .environment.environment_template_id = $t | .vault_ids = [$v]' openai/session.json \
        | api POST /agents/sessions --data-binary @- | jq -er .id)
    record session "$sid"
    jq -n --arg t "$question" \
        '{events: [{type: "agent.session.input.message", input: [{role: "user", content: [{type: "input_text", text: $t}]}]}]}' \
        | api POST "/agents/sessions/$sid/events" --data-binary @- >/dev/null
    echo "session $sid: waiting for the agent..."

    local status=none
    for _ in $(seq 200); do
        status=$(api GET "/agents/sessions/$sid/turns?order=desc&limit=1" | jq -r '.data[0].status // "none"')
        [[ "$status" =~ ^(completed|failed|cancelled)$ ]] && break
        sleep 3
    done
    local after="" page
    while :; do
        page=$(api GET "/agents/sessions/$sid/items?order=asc&limit=100${after:+&after=$after}")
        jq -r '.data[]
            | if .type == "mcp_call" then
                "\n> \(.server_label).\(.name) \(.arguments.sql // (.arguments | tostring))\n"
                + (if .error then "error: \(.error | tostring)"
                   else [.output.content[]?.text // empty] | join("") end)
              elif .type == "message" and .role == "assistant" then "\n[agent] \([.content[]?.text // empty] | join(" "))"
              else empty end' <<<"$page"
        [[ "$(jq -r .has_more <<<"$page")" == true ]] || break
        after=$(jq -r .last_id <<<"$page")
    done
    printf '\nturn: %s\n' "$status"
    [[ "$status" == completed ]] || die "turn status: $status"
}

# gone <path>: one deletion. Already gone counts as done; a running session
# answers 409 for a while.
gone() {
    local code
    for _ in $(seq 12); do
        code=$(oai -o /dev/null -w '%{http_code}' -X DELETE "https://api.openai.com/v1$1") || return 1
        case $code in
            2??|404) return 0 ;;
            409) sleep 5 ;;
            *) printf 'DELETE %s: HTTP %s\n' "$1" "$code" >&2; return 1 ;;
        esac
    done
    printf 'DELETE %s: still 409 after a minute\n' "$1" >&2
    return 1
}

delete() {
    [[ -s "$ids" ]] || { echo "nothing to delete: $ids lists no resources"; return; }
    : "${OPENAI_API_KEY:?set OPENAI_API_KEY}"
    local failed="" kind path v
    # The vault holds the token, so it goes first.
    for kind in vault session agent template; do
        case $kind in
            vault) path=/vaults ;;
            session) path=/agents/sessions ;;
            agent) path=/agents ;;
            template) path=/agents/environments/templates ;;
        esac
        for v in $(id "$kind"); do
            if gone "$path/$v"; then forget "$kind" "$v"; else failed+=" $kind $v"; fi
        done
    done
    [[ -z "$failed" ]] || die "could not delete:$failed; run ./setup.sh delete again"
    rm -f "$ids"
    echo "deleted the vault, sessions, agent and template"
}

case "${1:-}" in
    create) create "${@:2}" ;;
    session) session "${@:2}" ;;
    delete) delete ;;
    *) die "usage: ./setup.sh create [--dry-run] | session <question> | delete" ;;
esac
