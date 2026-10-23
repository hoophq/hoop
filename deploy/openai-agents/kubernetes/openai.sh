#!/usr/bin/env bash
#
# The OpenAI side of this template, with curl and jq: the kubectl tarball as
# a file, an environment template that reaches only the sidecar, a vault
# with the agent's token, and the agent. Each id goes to .openai as it is
# created, so delete also removes a partial create.
#
# Usage:
#   SIDECAR_HOST=<host> ./openai.sh create
#   ./openai.sh ask '<question>' [<dir>]   one session; <dir> keeps session.json and items.json
#   ./openai.sh delete                     everything .openai names
#
# OPENAI_API_KEY holds a key with Agents API access. create reads the
# agent's token from the cluster in KUBE_CONTEXT (default: the current
# context); MODEL overrides the model in openai/agent.json.

set -euo pipefail
cd "$(dirname "$0")"

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in curl jq kubectl shasum base64; do command -v "$t" >/dev/null || die "missing required tool: $t"; done
: "${OPENAI_API_KEY:?set OPENAI_API_KEY}"
IDS=${OPENAI_IDS:-.openai}
KUBECTL_VERSION=${KUBECTL_VERSION:-v1.35.5}

# api <method> <path> [curl args]: the key goes through a file descriptor, not argv.
api() {
    local m=$1 p=$2
    shift 2
    curl -sS --fail-with-body -X "$m" "https://api.openai.com/v1$p" \
        -H @<(printf 'Authorization: Bearer %s\n' "$OPENAI_API_KEY") -H 'OpenAI-Beta: agents=v1' "$@"
}
# post <path>: the JSON body on stdin; prints the new object's id.
post() {
    local out
    out=$(api POST "$1" -H 'Content-Type: application/json' --data-binary @-) || die "POST $1: $out"
    jq -r .id <<<"$out"
}
save() { (umask 077; printf '%s=%s\n' "$1" "$2" >> "$IDS"); }
ids() { [[ -f $IDS ]] && sed -n "s/^$1=//p" "$IDS" || true; }
host() { sed "s/sidecar\.example\.com/$SIDECAR_HOST/g" "$@"; }

create() {
    : "${SIDECAR_HOST:?set SIDECAR_HOST to the public host of the sidecar}"
    [[ ! -s $IDS ]] || die "$IDS exists: run ./openai.sh delete first"
    local token
    token=$(kubectl ${KUBE_CONTEXT:+--context "$KUBE_CONTEXT"} -n hoop get secret openai-agent-token \
        -o jsonpath='{.data.token}' | base64 -d)
    [[ -n "$token" ]] || die "secret hoop/openai-agent-token holds no token: apply rbac.yaml first"
    # From here on a failure can leave a vault that holds the token.
    trap '[[ $? == 0 ]] || echo "create failed: ./openai.sh delete removes what it made" >&2' EXIT

    # A file holds 50 MiB at most and kubectl is 56: send the release tarball.
    local tgz=.cache/kubernetes-client-$KUBECTL_VERSION-linux-amd64.tar.gz
    local url=https://dl.k8s.io/$KUBECTL_VERSION/kubernetes-client-linux-amd64.tar.gz
    if [[ ! -f $tgz ]]; then
        mkdir -p .cache
        curl -fsSL -o "$tgz.part" "$url"
        [[ "$(shasum -a 512 "$tgz.part" | cut -d' ' -f1)" == "$(curl -fsSL "$url.sha512")" ]] \
            || { rm -f "$tgz.part"; die "$url does not match its published SHA-512"; }
        mv "$tgz.part" "$tgz"
    fi
    local out file_id template_id vault_id agent_id
    out=$(api POST /files -F purpose=user_data -F "file=@$tgz") || die "POST /files: $out"
    file_id=$(jq -r .id <<<"$out")
    save FILE_ID "$file_id"

    template_id=$(host openai/template.json | jq --arg f "$file_id" \
        --arg d "$(host openai/kubeconfig.sh | base64 | tr -d '\n')" \
        '.files |= map(if .type == "file_id" then .file_id = $f else .data = $d end)' \
        | post /agents/environments/templates)
    save TEMPLATE_ID "$template_id"

    vault_id=$(post /vaults < openai/vault.json)
    save VAULT_ID "$vault_id"
    # --rawfile keeps the token out of argv; the API refuses a trailing newline.
    host openai/credential.json | jq --rawfile v <(printf %s "$token") '.auth.secret_value = $v' \
        | post "/vaults/$vault_id/credentials" >/dev/null

    agent_id=$(jq --rawfile i <(host openai/instructions.md) --arg m "${MODEL:-}" \
        '.instructions = $i | if $m != "" then .model = $m else . end' openai/agent.json \
        | post /agents)
    save AGENT_ID "$agent_id"
    trap - EXIT
    echo "file $file_id, template $template_id, vault $vault_id, agent $agent_id"
}

ask() {
    local question=${1:?usage: ./openai.sh ask '<question>' [<dir>]} dir=${2:-}
    local template_id vault_id agent_id session_id status=none items=() after="" page
    template_id=$(ids TEMPLATE_ID) vault_id=$(ids VAULT_ID) agent_id=$(ids AGENT_ID)
    [[ -n "$agent_id" ]] || die "no agent in $IDS: run ./openai.sh create first"

    session_id=$(jq -n --arg a "$agent_id" --arg t "$template_id" --arg v "$vault_id" '{
        agent_id: $a, vault_ids: [$v], metadata: {source: "hoop openai-agents/kubernetes"},
        environment: {type: "openai_hosted", environment_template_id: $t, container_size: "small"}}' \
        | post /agents/sessions)
    save SESSION_ID "$session_id"
    echo "session $session_id" >&2
    # The sandbox has no variable that holds the session id, so the first
    # message gives it, and kubeconfig.sh writes it into each request.
    jq -n --arg t "Your session id is $session_id. $question" '{events: [{type: "agent.session.input.message",
        input: [{role: "user", content: [{type: "input_text", text: $t}]}]}]}' \
        | post "/agents/sessions/$session_id/events" >/dev/null

    for _ in $(seq 120); do
        sleep 5
        # One failed poll is not the session's end: poll again.
        status=$(api GET "/agents/sessions/$session_id/turns?order=desc&limit=1" \
            | jq -r '.data[0].status // "none"') || status=unknown
        [[ "$status" =~ ^(completed|failed|cancelled)$ ]] && break
    done
    while :; do
        page=$(api GET "/agents/sessions/$session_id/items?order=asc&limit=100${after:+&after=$after}")
        items+=("$page")
        [[ "$(jq -r .has_more <<<"$page")" == true ]] || break
        after=$(jq -r .last_id <<<"$page")
    done
    page=$(printf '%s\n' "${items[@]}" | jq -s '[.[].data[]]')
    if [[ -n "$dir" ]]; then
        mkdir -p "$dir"
        printf '%s\n' "$page" > "$dir/items.json"
        api GET "/agents/sessions/$session_id" > "$dir/session.json"
    fi
    jq -r '.[] | if .type == "command_execution" then "\n$ \(.command)\n\(.output // "" | rtrimstr("\n"))"
        elif .type == "message" then "\n[\(.role)] \([.content[]?.text // empty] | join(" "))"
        else empty end' <<<"$page"
    [[ "$status" =~ ^(completed|failed|cancelled)$ ]] || die "the session's turn did not end within 10 minutes"
    [[ "$status" == completed ]] || die "the session's turn ended $status"
}

delete() {
    local failed="" id code
    drop() { # drop <label> <path>: a session still running answers 409 for a while
        for _ in $(seq 10); do
            code=$(api DELETE "$2" -o /dev/null -w '%{http_code}' 2>/dev/null) || true
            [[ "$code" =~ ^(200|204|404)$ ]] && return
            sleep 5
        done
        failed+=" $1"
    }
    for id in $(ids SESSION_ID); do drop "session $id" "/agents/sessions/$id"; done
    # The vault holds the token: it goes before what holds no secret.
    for id in $(ids VAULT_ID); do drop "vault $id" "/vaults/$id"; done
    for id in $(ids AGENT_ID); do drop "agent $id" "/agents/$id"; done
    for id in $(ids TEMPLATE_ID); do drop "template $id" "/agents/environments/templates/$id"; done
    for id in $(ids FILE_ID); do drop "file $id" "/files/$id"; done
    [[ -z "$failed" ]] || die "could not delete:$failed ($IDS keeps every id)"
    rm -f "$IDS"
    echo "deleted what $IDS named"
}

case "${1:-}" in
    create) create ;;
    ask) shift; ask "$@" ;;
    delete) delete ;;
    *) die "usage: ./openai.sh create | ask '<question>' [<dir>] | delete" ;;
esac
