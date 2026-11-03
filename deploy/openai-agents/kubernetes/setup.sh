#!/usr/bin/env bash
#
# Creates the OpenAI side of this template from the request bodies in
# openai/: the kubectl client tarball as a file, an environment template
# that reaches only the sidecar's host and carries that file, a vault with
# the agent's token bound to that host, and the agent. README.md has the
# steps.
#
# Usage:
#   ./setup.sh create                       create the file, template, vault, credential, agent
#   ./setup.sh create --dry-run             print those request bodies: no secret, no API call
#   ./setup.sh session <question> [<dir>]   start a session, ask <question>, print the turn;
#                                           <dir> keeps session.json and items.json
#   ./setup.sh delete                       delete what create and session made
#
# Reads SIDECAR_HOST (create), OPENAI_API_KEY (all but --dry-run). create
# reads the agent's token from the cluster in KUBE_CONTEXT (default: the
# current context); KUBECTL_VERSION picks kubectl and MODEL the agent's
# model. Ids go to OPENAI_IDS, default ./.openai-ids. Needs curl 7.76 or
# later and jq 1.6 or later; create also needs kubectl and shasum.

set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
ids=${OPENAI_IDS:-.openai-ids}
KUBECTL_VERSION=${KUBECTL_VERSION:-v1.35.5}

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in curl jq base64; do command -v "$t" >/dev/null || die "missing required tool: $t"; done

# oai <curl args>: the key goes through a file descriptor, not argv.
oai() {
    curl -sS -H @<(printf 'Authorization: Bearer %s\n' "$OPENAI_API_KEY") -H 'OpenAI-Beta: agents=v1' "$@"
}

# api <method> <path> [curl args]: one JSON request; prints the response, or
# fails with it.
api() {
    local m=$1 p=$2 out
    shift 2
    out=$(oai --fail-with-body -H 'Content-Type: application/json' -X "$m" "https://api.openai.com/v1$p" "$@") \
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
    jq --arg h "$SIDECAR_HOST" --rawfile i "$here/openai/agent.md" --arg m "${MODEL:-}" \
        '.instructions = $i | (.. | strings) |= gsub("sidecar\\.example\\.com"; $h)
         | if $m != "" then .model = $m else . end' "$here/openai/agent.json"
}
# template_body <file id>: the template carries the tarball and kubeconfig.sh.
template_body() {
    body environment-template.json | jq --arg f "$1" \
        --arg d "$(sed "s/sidecar\.example\.com/$SIDECAR_HOST/g" "$here/openai/kubeconfig.sh" | base64 | tr -d '\n')" \
        '.files |= map(if .type == "file_id" then .file_id = $f else .data = $d end)'
}

# kubectl_tarball: prints the path of the release's client tarball. kubectl
# is 56 MiB and a file holds 50 MiB at most; the tarball is 33 MiB.
kubectl_tarball() {
    local tgz=$here/.cache/kubernetes-client-$KUBECTL_VERSION-linux-amd64.tar.gz
    local url=https://dl.k8s.io/$KUBECTL_VERSION/kubernetes-client-linux-amd64.tar.gz sum
    sum=$(curl -fsSL "$url.sha512") || die "no published SHA-512 at $url.sha512"
    # Checked on each create, not only on download: the sandbox runs this file.
    if [[ ! -f $tgz || "$(shasum -a 512 "$tgz" | cut -d' ' -f1)" != "$sum" ]]; then
        mkdir -p "$here/.cache"
        curl -fsSL -o "$tgz.part" "$url"
        [[ "$(shasum -a 512 "$tgz.part" | cut -d' ' -f1)" == "$sum" ]] \
            || { rm -f "$tgz.part"; die "$url does not match its published SHA-512"; }
        mv "$tgz.part" "$tgz"
    fi
    printf '%s\n' "$tgz"
}

create() {
    : "${SIDECAR_HOST:?set SIDECAR_HOST to the public host of the sidecar}"
    [[ "$SIDECAR_HOST" =~ ^[A-Za-z0-9.-]+$ ]] \
        || die "SIDECAR_HOST must be a bare host name: no scheme, port or path"
    if [[ "${1:-}" == --dry-run ]]; then
        jq -n --argjson t "$(template_body file-dry-run)" --argjson v "$(body vault.json)" \
            --argjson c "$(jq -s . <(body credentials/kube-token.json))" \
            --argjson a "$(agent_body)" '{environment_template: $t, vault: $v, credentials: $c, agent: $a}'
        return
    fi
    [[ -z "${1:-}" ]] || die "unknown argument: $1"
    : "${OPENAI_API_KEY:?set OPENAI_API_KEY}"
    for t in kubectl shasum; do command -v "$t" >/dev/null || die "missing required tool: $t"; done
    [[ ! -s "$ids" ]] || die "$ids already lists resources: run ./setup.sh delete first"
    local token tgz out file template vault agent
    token=$(kubectl ${KUBE_CONTEXT:+--context "$KUBE_CONTEXT"} -n hoop get secret openai-agent-token \
        -o jsonpath='{.data.token}' | base64 -d)
    [[ -n "$token" ]] || die "secret hoop/openai-agent-token holds no token: apply rbac.yaml first"
    tgz=$(kubectl_tarball)
    # From here on a failure can leave a vault that holds the token.
    trap '[[ $? == 0 ]] || echo "create failed: ./setup.sh delete removes what it made" >&2' EXIT

    out=$(oai --fail-with-body -X POST https://api.openai.com/v1/files -F purpose=user_data -F "file=@$tgz") \
        || die "POST /files: $out"
    file=$(jq -er .id <<<"$out")
    record file "$file"
    template=$(template_body "$file" | api POST /agents/environments/templates --data-binary @- | jq -er .id)
    record template "$template"
    vault=$(body vault.json | api POST /vaults --data-binary @- | jq -er .id)
    record vault "$vault"
    # --rawfile keeps the token out of argv; the API refuses a trailing newline.
    body credentials/kube-token.json | jq --rawfile v <(printf %s "$token") '.auth.secret_value = $v' \
        | api POST "/vaults/$vault/credentials" --data-binary @- >/dev/null
    agent=$(agent_body | api POST /agents --data-binary @- | jq -er .id)
    record agent "$agent"
    trap - EXIT
    echo "created file $file, template $template, vault $vault, agent $agent for https://$SIDECAR_HOST"
}

session() {
    local question=${1:?usage: ./setup.sh session <question> [<dir>]} dir=${2:-}
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
    # gives the id; kubeconfig.sh writes it into each request.
    jq -n --arg t "Your session id is $sid. $question" \
        '{events: [{type: "agent.session.input.message", input: [{role: "user", content: [{type: "input_text", text: $t}]}]}]}' \
        | api POST "/agents/sessions/$sid/events" --data-binary @- >/dev/null
    echo "session $sid: waiting for the agent..." >&2

    local status=none
    for _ in $(seq 200); do
        sleep 3
        # One failed poll is not the turn's end: poll again.
        status=$(api GET "/agents/sessions/$sid/turns?order=desc&limit=1" | jq -r '.data[0].status // "none"') \
            || status=unknown
        [[ "$status" =~ ^(completed|failed|cancelled)$ ]] && break
    done
    local after="" page items="[]"
    while :; do
        page=$(api GET "/agents/sessions/$sid/items?order=asc&limit=100${after:+&after=$after}")
        # Files, not --argjson: a page of kubectl output can pass ARG_MAX.
        items=$(jq -cs '.[0] + .[1].data' <(printf '%s' "$items") <(printf '%s' "$page"))
        [[ "$(jq -r .has_more <<<"$page")" == true ]] || break
        after=$(jq -r .last_id <<<"$page")
    done
    if [[ -n "$dir" ]]; then
        mkdir -p "$dir"
        printf '%s\n' "$items" > "$dir/items.json"
        api GET "/agents/sessions/$sid" > "$dir/session.json"
    fi
    jq -r '.[]
        | if .type == "command_execution" then "\n$ \(.command)\n\(.output // "" | rtrimstr("\n"))"
          elif .type == "message" and .role == "assistant" then "\n[agent] \([.content[]?.text // empty] | join(" "))"
          else empty end' <<<"$items"
    printf '\nturn: %s\n' "$status"
    [[ "$status" =~ ^(completed|failed|cancelled)$ ]] || die "the turn did not end within 10 minutes"
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
    for kind in vault session agent template file; do
        case $kind in
            vault) path=/vaults ;;
            session) path=/agents/sessions ;;
            agent) path=/agents ;;
            template) path=/agents/environments/templates ;;
            file) path=/files ;;
        esac
        for v in $(id "$kind"); do
            if gone "$path/$v"; then forget "$kind" "$v"; else failed+=" $kind $v"; fi
        done
    done
    [[ -z "$failed" ]] || die "could not delete:$failed; run ./setup.sh delete again"
    rm -f "$ids"
    echo "deleted the vault, sessions, agent, template and file"
}

case "${1:-}" in
    create) create "${@:2}" ;;
    session) session "${@:2}" ;;
    delete) delete ;;
    *) die "usage: ./setup.sh create [--dry-run] | session <question> [<dir>] | delete" ;;
esac
