#!/usr/bin/env bash
#
# Creates the Managed Agents side of the quick start for the host ./run.sh
# found: an environment that can reach only that host, a vault with the
# caller token, the demo agent. Then it starts a session and asks the agent
# to read the users and to try one DELETE.
#
# A quick tunnel gets a new host on each start, so a second run deletes what
# the first made, then creates it again for the current host.
#
# Usage:
#   ./agent.sh            create, then start a session
#   ./agent.sh --delete   delete the session, agent, vault and environment
#
# Prereqs: ./run.sh, ant 1.42.0 or later signed in (ANTHROPIC_API_KEY or
# `ant auth login`), jq.
# MODEL=claude-opus-5-5 ./agent.sh overrides the agent file's model.

set -euo pipefail
cd "$(dirname "$0")"

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in ant jq; do command -v "$t" >/dev/null || die "missing required tool: $t"; done

LOCK=.work/ant/claude-lock.json
AGENT=agents/demo-api.md ENVIRONMENT=environments/hoop-sidecar.yaml VAULT=vaults/hoop-sidecar.yaml
lockid() { jq -r --arg f "./$1" '.resources[$f].id // empty' "$LOCK"; }

# gone <ant args>: one removal. A resource already removed, in the console
# for one, counts as done: the API answers it with not_found_error.
gone() {
    local out
    out=$(ant "$@" 2>&1) && return 0
    grep -qE '"type": *"not_found_error"' <<<"$out" && return 0
    printf '%s\n' "$out" >&2
    return 1
}

# drop <lock file key> <ant args, ending with the id flag>. A deleted resource
# leaves the lock, so a retry after a failure touches only what is left.
drop() {
    local id rest
    id=$(lockid "$1") || return 1
    [[ -n "$id" ]] || return 0
    gone "${@:2}" "$id" || return 1
    rest=$(jq --arg f "./$1" 'del(.resources[$f])' "$LOCK") && printf '%s\n' "$rest" > "$LOCK"
}

# The API refuses to archive a running session, so interrupt it and wait.
archive_session() {
    local id status
    id=$(cat .work/session)
    status() { ant beta:sessions retrieve --session-id "$id" --transform status --raw-output 2>/dev/null; }
    if [[ "$(status)" == running ]]; then
        ant beta:sessions:events send --session-id "$id" --event '{type: user.interrupt}' >/dev/null
        for _ in $(seq 30); do [[ "$(status)" != running ]] && break; sleep 1; done
    fi
    gone beta:sessions archive --session-id "$id" && rm .work/session
}

delete_all() {
    [[ -f "$LOCK" ]] || return 0
    local failed=""
    # The vault holds the token, so it goes first.
    drop "$VAULT" beta:vaults delete --vault-id || failed+=" vault"
    if [[ -s .work/session ]]; then archive_session || failed+=" session"; fi
    drop "$AGENT" beta:agents archive --agent-id || failed+=" agent"
    drop "$ENVIRONMENT" beta:environments delete --environment-id || failed+=" environment"
    [[ -z "$failed" ]] || die "could not delete:$failed; run ./agent.sh --delete again (ids in .work/)"
    rm -rf .work/ant
}

if [[ "${1:-}" == "--delete" ]]; then
    delete_all
    echo "deleted the session, agent, vault and environment"
    exit 0
fi
[[ -z "${1:-}" ]] || die "unknown argument: $1 (see the header of $0)"

HOST=$(cat .work/host 2>/dev/null || true)
[[ -n "$HOST" ]] || die "no tunnel host: run ./run.sh first"
delete_all

# The committed files keep the placeholder; the host goes into a copy. The
# environment, vault and credential are the helm path's own files.
fill() {
    sed -e "s/sidecar\.example\.com/$HOST/g" -e "${MODEL:+s/^model: .*/model: $MODEL/}" "$1"
}
mkdir -p .work/ant/agents .work/ant/environments .work/ant/vaults
fill "ant/$AGENT" > ".work/ant/$AGENT"
fill "../ant/$ENVIRONMENT" > ".work/ant/$ENVIRONMENT"
fill "../ant/$VAULT" > ".work/ant/$VAULT"

(cd .work/ant && ant apply --yes . >/dev/null)
AGENT_ID=$(lockid "$AGENT") ENV_ID=$(lockid "$ENVIRONMENT") VAULT_ID=$(lockid "$VAULT")
[[ -n "$AGENT_ID" && -n "$ENV_ID" && -n "$VAULT_ID" ]] || die "ant apply recorded no ids in $LOCK"
# tr: the API refuses a trailing newline. @<(...) keeps the token out of the
# process list.
fill ../credentials/caller-token.yaml | ant beta:vaults:credentials create --vault-id "$VAULT_ID" \
    --auth.secret-value @<(tr -d '\n' < .secrets/caller-token) >/dev/null

# The sandbox has no variable that names its session, so the first message
# gives the id; the agent sends it on every request for the audit trail.
SESSION_ID=$(ant beta:sessions create --agent "$AGENT_ID" --environment-id "$ENV_ID" \
    --vault-id "$VAULT_ID" --title "hoop quick start" \
    --budget '{type: limit, max_list_cost: {amount: "5", currency: USD}}' \
    --transform id --raw-output)
echo "$SESSION_ID" > .work/session
PROMPT="Your session id is $SESSION_ID. This checks the API's access policy. \
1) List the users and give each one's name, email, phone, card and plan exactly as the API returns them. \
2) Delete user 1 with DELETE /users/1 and report the status and the body as returned. \
The policy is expected to refuse it; do not retry it."
ant beta:sessions:events send --session-id "$SESSION_ID" \
    --event "$(jq -nc --arg t "$PROMPT" '{type: "user.message", content: [{type: "text", text: $t}]}')" >/dev/null

cat <<EOF
Session $SESSION_ID started against https://$HOST. Watch the agent:

  ant beta:sessions connect --session-id $SESSION_ID
EOF
