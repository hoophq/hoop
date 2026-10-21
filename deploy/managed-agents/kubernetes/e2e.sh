#!/usr/bin/env bash
#
# The real end to end: kubectl in a Managed Agents cloud session reaches the
# k3d cluster through the sidecar's quick tunnel, and fails to reach the API
# server's own tunnel. It applies this template's ant and credential files,
# so it checks them against the live API too. The tunnel host replaces
# sidecar.example.com in a temporary copy.
#
# It uploads the official kubectl for linux/amd64 (checked against the
# release's digest) and mounts it in the session, writes evidence/<time>/,
# then deletes the vault, the environment and the file it made and archives
# the agent and the session; --keep leaves them.
#
# Prereqs: ./run.sh --tunnel, ant 1.42.0 or later, jq, curl, kubectl,
# ANTHROPIC_API_KEY. MODEL=claude-opus-5-5 ./e2e.sh overrides the agent
# file's model.

set -euo pipefail
cd "$(dirname "$0")"

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in ant jq curl kubectl shasum; do command -v "$t" >/dev/null || die "missing required tool: $t"; done
[[ -n "${ANTHROPIC_API_KEY:-}" ]] || die "set ANTHROPIC_API_KEY"
[[ -f .env ]] || die "no .env: run ./run.sh --tunnel first"
CTX=$(sed -n 's/^CONTEXT=//p' .env)
SIDECAR_HOST=$(sed -n 's/^SIDECAR_HOST=//p' .env)
APISERVER_HOST=$(sed -n 's/^APISERVER_HOST=//p' .env)
[[ -n "$SIDECAR_HOST" && -n "$APISERVER_HOST" ]] || die "no tunnel hosts in .env: run ./run.sh --tunnel first"
kc() { kubectl --context "$CTX" "$@"; }
# A quick tunnel can expire while its container runs on; its host then
# resolves nowhere. The API server answers /version with 200 when anonymous
# access is on (the Kubernetes default) and 401 when it is off (k3s).
api_answers() { [[ "$(curl -s -m 10 -o /dev/null -w '%{http_code}' "https://$1/version")" =~ ^(200|401)$ ]]; }
for h in "$SIDECAR_HOST" "$APISERVER_HOST"; do
    api_answers "$h" || die "https://$h does not answer: run ./run.sh --tunnel again"
done

KEEP=""; [[ "${1:-}" == "--keep" ]] && KEEP=1
RUN=r$(date +%s)
OUT="evidence/$(date -u +%Y%m%dT%H%M%SZ)"
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
SECRET_VALUE='hunter2-demo-value' # run.sh's demo Secret
REFUSAL='this agent may not delete cluster objects'
TOKEN=$(kc -n hoop get secret claude-agent-token -o jsonpath='{.data.token}' | base64 -d)
mkdir -p "$OUT"

# ----------------------------------------------------------------- kubectl
# The sandbox has no kubectl and no setup step, and its egress reaches only
# the sidecar: the binary goes in as a session file.
KUBECTL_VERSION=v1.35.5
BIN=.cache/kubectl-$KUBECTL_VERSION-linux-amd64
if [[ ! -f "$BIN" ]]; then
    mkdir -p .cache
    base=https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64
    curl -fsSL -o "$BIN.part" "$base/kubectl"
    [[ "$(shasum -a 256 "$BIN.part" | cut -d' ' -f1)" == "$(curl -fsSL "$base/kubectl.sha256")" ]] \
        || { rm -f "$BIN.part"; die "kubectl $KUBECTL_VERSION does not match its published SHA-256"; }
    mv "$BIN.part" "$BIN"
fi

# ------------------------------------------------------------------ create
# Only the files this script creates and removes: never a committed
# claude-lock.json, or apply would update your real resources and the
# cleanup would delete them.
AGENT=agents/kubernetes-operator.md ENVIRONMENT=environments/hoop-kubernetes.yaml VAULT=vaults/hoop-kubernetes.yaml
WORK=$(mktemp -d)
mkdir -p "$WORK/ant/agents" "$WORK/ant/environments" "$WORK/ant/vaults"
for f in "$AGENT" "$ENVIRONMENT" "$VAULT"; do cp "ant/$f" "$WORK/ant/$f"; done
cp credentials/kube-token.yaml "$WORK/"
cp "$BIN" "$WORK/kubectl" # the upload keeps the last path component as its name
sed -i.bak "s/sidecar\.example\.com/$SIDECAR_HOST/g" \
    "$WORK/ant/$AGENT" "$WORK/ant/$ENVIRONMENT" "$WORK/kube-token.yaml"
[[ -z "${MODEL:-}" ]] || sed -i.bak "s/^model: .*/model: $MODEL/" "$WORK/ant/$AGENT"

AGENT_ID="" VAULT_ID="" ENV_ID="" SESSION_ID="" FILE_ID="" FAILED_CLEANUP=""
drop() { # drop <label> <id> <ant args, ending with the id flag>
    local label=$1 id=$2; shift 2
    [[ -z "$id" ]] || ant "$@" "$id" >/dev/null || FAILED_CLEANUP+=" $label"
}
cleanup() {
    rm -rf "$WORK"
    [[ -z "$KEEP" ]] || { echo "kept: agent $AGENT_ID, vault $VAULT_ID, environment $ENV_ID, file $FILE_ID"; return; }
    # The vault holds the token, so it goes first.
    drop vault "$VAULT_ID" beta:vaults delete --vault-id
    drop session "$SESSION_ID" beta:sessions archive --session-id
    drop agent "$AGENT_ID" beta:agents archive --agent-id
    drop environment "$ENV_ID" beta:environments delete --environment-id
    drop file "$FILE_ID" beta:files delete --file-id
    if [[ -n "$FAILED_CLEANUP" ]]; then echo "cleanup failed for:$FAILED_CLEANUP" >&2; return 1; fi
    echo "cleaned up the vault, session, agent, environment and file"
}
# A vault left behind holds the live token, so a failed cleanup fails the run.
on_exit() { local rc=$?; cleanup || rc=1; exit "$rc"; }
trap on_exit EXIT

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
    --auth.secret-value @<(printf %s "$TOKEN") < "$WORK/kube-token.yaml" >/dev/null
FILE_ID=$(ant beta:files upload --file "$WORK/kubectl" --expires-in-seconds 3600 --transform id --raw-output)
echo "agent $AGENT_ID, environment $ENV_ID, vault $VAULT_ID, kubectl $FILE_ID"

# ----------------------------------------------------------------- session
PODS_BEFORE=$(kc -n demo get pods -o jsonpath='{.items[*].metadata.uid}')
PROMPT="This checks the cluster policy on fixture objects in namespace demo. \
Run the setup from your instructions, then each command below exactly as \
written, one bash call each, in order, and report each output as returned. \
The label selector 'e2e notin (...)' matches every object; it only tags the \
request in the audit trail. The policy is expected to refuse the delete; do \
not retry it. Command 4 counts HTTP/2 headers in kubectl's debug log. Command 5 \
is a reachability check of a host that the sandbox is expected to block.

1. kubectl get pods -n demo -l 'e2e notin ($RUN-1)'
2. kubectl delete pod -n demo -l 'app=web,e2e notin ($RUN-2)' --wait=false
3. kubectl get secret -n demo -l 'e2e notin ($RUN-3)' -o yaml
4. GODEBUG=http2debug=1 kubectl get pods -n demo -l 'e2e notin ($RUN-4)' 2>&1 | grep -c 'http2: Transport encoding header'
5. curl -sS -m 10 'https://$APISERVER_HOST/version?e2e=$RUN-5' 2>&1"

SESSION_ID=$(ant beta:sessions create --agent "$AGENT_ID" --environment-id "$ENV_ID" \
    --vault-id "$VAULT_ID" --title "hoop kubernetes e2e $RUN" \
    --resource "{type: file, file_id: $FILE_ID, mount_path: /kubectl}" \
    --budget '{type: limit, max_list_cost: {amount: "100", currency: USD}}' \
    --initial-event "$(jq -nc --arg t "$PROMPT" '{type: "user.message", content: [{type: "text", text: $t}]}')" \
    --transform id --raw-output)
echo "session $SESSION_ID"

events() { ant beta:sessions:events list --session-id "$SESSION_ID" --max-items -1 --format jsonl; }
FINISHED="" STOP=""
for _ in $(seq 120); do
    sleep 5
    last=$(events | jq -c 'select(.type | startswith("session."))' | tail -1)
    STOP=$(jq -r '.type' <<<"$last")
    case "$STOP" in
    session.status_idle)
        [[ "$(jq -r '.stop_reason.type // ""' <<<"$last")" == requires_action ]] || { FINISHED=1; break; } ;;
    session.error|session.status_terminated) FINISHED=1; break ;;
    esac
done
events > "$OUT/events.jsonl"

# --------------------------------------------------------------- evidence
jq -r '
    if   .type == "user.message"      then "## user\n\n" + ([.content[].text] | join("")) + "\n"
    elif .type == "agent.tool_use"    then "### \(.name)\n\n```bash\n\(.input.command // (.input | tostring))\n```\n"
    elif .type == "agent.tool_result" then "### result\(if .is_error then " (error)" else "" end)\n\n```\n" + ([.content[]?.text] | join("")) + "\n```\n"
    elif .type == "agent.message"     then "## agent\n\n" + ([.content[]?.text] | join("")) + "\n"
    elif .type == "session.error"     then "## session.error\n\n\(. | tostring)\n"
    else empty end' "$OUT/events.jsonl" > "$OUT/transcript.md"
# `|| true`: no row is a result to report, not a reason to stop here.
{ kc -n hoop logs deploy/hoop-kube-hoopsidecar --since-time="$SINCE" | grep '^{"kind' || true; } > "$OUT/audit.jsonl"

# ------------------------------------------------------------------ checks
count() { wc -l | tr -d ' '; }
expect() { # expect <got> <want> <description>
    if [[ "$1" == "$2" ]]; then echo "PASS $3"; else echo "FAIL $3 (expected '$2', got '$1')"; fi
}
of_type() { jq -c --arg t "$1" 'select(.type == $t)' "$OUT/events.jsonl"; }
# The output of the bash call whose command holds tag <n>, matched by its id.
output_of() {
    local id
    id=$(of_type agent.tool_use | jq -r --arg t "$RUN-$1" 'select(.input.command // "" | contains($t)) | .id' | head -1)
    of_type agent.tool_result | jq -r --arg id "$id" 'select(.tool_use_id == $id) | [.content[]?.text] | join("")'
}
rows() { jq -c --arg t "$RUN-$1" 'select(.direction == "client" and (.statement // "" | contains($t)))' "$OUT/audit.jsonl"; }
# The rows logged after the request tagged <a> and before the one tagged <b>.
# Log order, so a run before or after this one adds none.
between() { jq -sc --arg a "$RUN-$1" --arg b "$RUN-$2" '
    def at($t): map(.direction == "client" and (.statement // "" | contains($t))) | index(true);
    at($a) as $i | at($b) as $j | if $i and $j then .[$i + 1:$j][] else empty end' "$OUT/audit.jsonl"; }
# The server rows that answer the request tagged <n>: same session, after its
# client row, before that session's next client row.
replies() { jq -sc --arg t "$RUN-$1" '
    (to_entries | map(select(.value.direction == "client" and (.value.statement // "" | contains($t)))) | first) as $req
    | if $req == null then empty else
        (to_entries | map(select(.key > $req.key and .value.session_id == $req.value.session_id)) | map(.value)) as $after
        | ($after | map(.direction == "client") | index(true) // length) as $next
        | $after[:$next][] | select(.direction == "server")
      end' "$OUT/audit.jsonl" | jq -r .kind | paste -sd, -; }
refused_deletes() { jq -c 'select(.kind == "violation" and .rule == "no-delete" and (.statement | startswith("DELETE /api/v1/namespaces/demo/pods/web-")))'; }
direct=$(api_answers "$APISERVER_HOST" && echo yes || echo no)

{
    expect "$FINISHED" 1 "the session finished within 10 minutes"
    expect "$STOP" session.status_idle "the session ended idle, not terminated or in error"
    expect "$(jq -rs '[.[] | select(.type | startswith("agent."))] | last | .type' "$OUT/events.jsonl")" agent.message \
        "the agent answered after its last tool call"
    expect "$(of_type session.error | count)" 0 "no session error"
    for n in 1 2 3 4 5; do
        expect "$(of_type agent.tool_use | jq -c --arg t "$RUN-$n" 'select(.input.command // "" | contains($t))' | count)" 1 \
            "command $n ran once"
    done
    expect "$(output_of 1 | grep -c '^web-')" 2 "kubectl get pods lists the 2 demo pods"
    expect "$(output_of 2 | grep -c "Error from server (Forbidden): $REFUSAL")" 2 \
        "kubectl delete pod is refused for each pod, with the sidecar's message"
    expect "$(kc -n demo get pods -o jsonpath='{.items[*].metadata.uid}')" "$PODS_BEFORE" \
        "the pods still run: no delete reached the API server"
    expect "$(output_of 3 | grep -c "password: '\[REDACTED:K8S_DATA\]'")" 1 \
        "kubectl get secret -o yaml shows the password masked"
    expect "$(grep -cE "$SECRET_VALUE|$(printf %s "$SECRET_VALUE" | base64)" "$OUT/events.jsonl")" 0 \
        "no Secret value, plain or base64, reaches the session"
    expect "$(grep -cF -f <(printf '%s\n' "$TOKEN") "$OUT/events.jsonl")" 0 "the real token never appears in the session"
    expect "$(output_of 5 | grep -cE 'Could not resolve host|403')" 1 \
        "the direct call to the API server is refused inside the sandbox"
    expect "$direct" yes "the same API server answers from outside the sandbox"
    expect "$(rows 1 | jq -r '[.kind, .allowed] | join(" ")')" "statement true" "get pods: one row, admitted"
    expect "$(replies 1)" statement "get pods: one server row for its response"
    expect "$(rows 2 | count)" 1 "delete pod: one row for the list kubectl sends first"
    expect "$(between 2 3 | refused_deletes | count)" 2 \
        "delete pod: one violation row per refused DELETE"
    expect "$(rows 3 | count)" 1 "get secret: one row"
    expect "$(replies 3)" statement,masked "get secret: one server row and one masked row for its response"
    expect "$(rows 5 | count)" 0 "the direct call left no row: it never reached the sidecar"
    # Not a pass or fail: what the egress proxy negotiated with kubectl.
    headers=$(output_of 4 | tr -dc '0-9')
    echo "INFO kubectl in the sandbox: $([[ "${headers:-0}" -gt 0 ]] && echo "HTTP/2 ($headers headers written)" || echo "HTTP/1.1")"
} | tee "$OUT/result.txt"

echo "evidence: $OUT"
! grep -q '^FAIL' "$OUT/result.txt"
