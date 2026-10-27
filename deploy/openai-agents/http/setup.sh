#!/usr/bin/env bash
#
# Creates the OpenAI side of this template from the request bodies in
# openai/: an environment template that reaches only the sidecar's host, a
# vault with two credentials bound to that host, and the agent. README.md has
# the steps.
#
# Usage:
#   ./setup.sh create              create the template, vault, credentials, agent
#   ./setup.sh create --dry-run    print those request bodies: no secret, no API call
#   ./setup.sh session <question>  start a session, ask <question>, print the turn
#   ./setup.sh delete              delete what create and session made
#
# create --no-api-token makes no CUSTOMER_API_TOKEN credential, for an API
# that takes no token, such as the quick start's demo API.
#
# Reads SIDECAR_HOST (create), CALLER_TOKEN_FILE and API_TOKEN_FILE (create),
# OPENAI_API_KEY (all but --dry-run). Ids go to OPENAI_IDS, default
# ./.openai-ids. AGENT_DIR holds agent.json and agent.md, default openai/.
# Needs curl and jq 1.6 or later.

set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
ids=${OPENAI_IDS:-.openai-ids}
agent_dir=${AGENT_DIR:-$here/openai}

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in curl jq; do command -v "$t" >/dev/null || die "missing required tool: $t"; done

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

# The committed files name sidecar.example.com; each request names SIDECAR_HOST.
body() { jq --arg h "$SIDECAR_HOST" '(.. | strings) |= gsub("sidecar\\.example\\.com"; $h)' "$here/openai/$1"; }
agent_body() {
    jq --arg h "$SIDECAR_HOST" --rawfile i "$agent_dir/agent.md" \
        '.instructions = $i | (.. | strings) |= gsub("sidecar\\.example\\.com"; $h)' "$agent_dir/agent.json"
}

# credential <file in openai/> <secret file> <vault id>: --rawfile keeps the
# secret out of argv; the API refuses a trailing newline.
credential() {
    body "$1" | jq --rawfile v "$2" '.auth.secret_value = ($v | rtrimstr("\n"))' \
        | api POST "/vaults/$3/credentials" --data-binary @- >/dev/null
}

create() {
    local dry="" api_token=1 a
    for a in "$@"; do
        case $a in
            --dry-run) dry=1 ;;
            --no-api-token) api_token="" ;;
            *) die "unknown argument: $a" ;;
        esac
    done
    : "${SIDECAR_HOST:?set SIDECAR_HOST to the public host of the sidecar}"
    [[ "$SIDECAR_HOST" =~ ^[A-Za-z0-9.-]+$ ]] \
        || die "SIDECAR_HOST must be a bare host name: no scheme, port or path"
    if [[ -n "$dry" ]]; then
        jq -n --argjson t "$(body environment-template.json)" --argjson v "$(body vault.json)" \
            --argjson c "$({ body credentials/caller-token.json; [[ -z "$api_token" ]] || body credentials/customer-api-token.json; } | jq -s .)" \
            --argjson a "$(agent_body)" '{environment_template: $t, vault: $v, credentials: $c, agent: $a}'
        return
    fi
    : "${OPENAI_API_KEY:?set OPENAI_API_KEY}"
    local files f
    files=("${CALLER_TOKEN_FILE:?set CALLER_TOKEN_FILE}")
    [[ -z "$api_token" ]] || files+=("${API_TOKEN_FILE:?set API_TOKEN_FILE, or pass --no-api-token}")
    for f in "${files[@]}"; do
        [[ -s "$f" ]] || die "$f is missing or empty"
    done
    [[ ! -s "$ids" ]] || die "$ids already lists resources: run ./setup.sh delete first"

    local template vault agent
    template=$(body environment-template.json | api POST /agents/environments/templates --data-binary @- | jq -er .id)
    record template "$template"
    vault=$(body vault.json | api POST /vaults --data-binary @- | jq -er .id)
    record vault "$vault"
    credential credentials/caller-token.json "$CALLER_TOKEN_FILE" "$vault"
    [[ -z "$api_token" ]] || credential credentials/customer-api-token.json "$API_TOKEN_FILE" "$vault"
    agent=$(agent_body | api POST /agents --data-binary @- | jq -er .id)
    record agent "$agent"
    echo "created template $template, vault $vault, agent $agent for https://$SIDECAR_HOST"
}

session() {
    local question=${1:?usage: ./setup.sh session <question>}
    : "${OPENAI_API_KEY:?set OPENAI_API_KEY}"
    local template vault agent sid
    template=$(id template) vault=$(id vault) agent=$(id agent)
    [[ -n "$template" && -n "$vault" && -n "$agent" ]] || die "no resources in $ids: run ./setup.sh create first"

    # A template cannot hold a vault, so each session names it.
    sid=$(jq --arg t "$template" --arg v "$vault" --arg a "$agent" \
        '.agent_id = $a | .environment.environment_template_id = $t | .vault_ids = [$v]' "$here/openai/session.json" \
        | api POST /agents/sessions --data-binary @- | jq -er .id)
    record session "$sid"
    # The sandbox has no variable that names its session, so the first message
    # gives the id; the agent sends it on every request for the audit trail.
    jq -n --arg t "Your session id is $sid. $question" \
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
            | if .type == "command_execution" then "\n$ \(.command)\n\(.output // "" | rtrimstr("\n"))"
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
    # The vault holds the tokens, so it goes first.
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
    *) die "usage: ./setup.sh create [--dry-run] [--no-api-token] | session <question> | delete" ;;
esac
