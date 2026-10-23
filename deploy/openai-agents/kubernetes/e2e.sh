#!/usr/bin/env bash
#
# The real end to end: kubectl in an OpenAI-hosted session reaches the k3d
# cluster through the sidecar's quick tunnel, and fails to reach the API
# server's own tunnel. It creates the OpenAI side with ./openai.sh, so it
# checks openai/ and openai.sh against the live API too, with ids in a
# temporary file: never your .openai.
#
# It writes evidence/<time>/, then deletes the session, vault, agent,
# template and file it made; --keep leaves them.
#
# Prereqs: ./run.sh --tunnel, jq, curl, kubectl, OPENAI_API_KEY.
# MODEL=<model> ./e2e.sh overrides the agent's model.

set -euo pipefail
cd "$(dirname "$0")"

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in jq curl kubectl; do command -v "$t" >/dev/null || die "missing required tool: $t"; done
[[ -n "${OPENAI_API_KEY:-}" ]] || die "set OPENAI_API_KEY"
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
TOKEN=$(kc -n hoop get secret openai-agent-token -o jsonpath='{.data.token}' | base64 -d)
mkdir -p "$OUT"

# ------------------------------------------------------------------ create
export OPENAI_IDS
OPENAI_IDS=$(mktemp)
cleanup() {
    if [[ -n "$KEEP" ]]; then echo "kept: $(tr '\n' ' ' < "$OPENAI_IDS")"; return; fi
    ./openai.sh delete
}
# A vault left behind holds the live token, so a failed cleanup fails the run.
on_exit() { local rc=$?; cleanup || rc=1; exit "$rc"; }
trap on_exit EXIT
SIDECAR_HOST=$SIDECAR_HOST KUBE_CONTEXT=$CTX ./openai.sh create

# ----------------------------------------------------------------- session
PODS_BEFORE=$(kc -n demo get pods -o jsonpath='{.items[*].metadata.uid}')
PROMPT="This checks the cluster policy on fixture objects in namespace demo. \
Run the setup from your instructions, then each command below exactly as \
written, one shell call each, in order, and report each output as returned. \
The label selector 'e2e notin (...)' matches every object; it only tags the \
request in the audit trail. The policy is expected to refuse the delete; do \
not retry it. Command 4 counts HTTP/2 headers in kubectl's debug log. Command 5 \
is a reachability check of a host that the sandbox is expected to block.

1. kubectl get pods -n demo -l 'e2e notin ($RUN-1)'
2. kubectl delete pod -n demo -l 'app=web,e2e notin ($RUN-2)' --wait=false
3. kubectl get secret -n demo -l 'e2e notin ($RUN-3)' -o json
4. GODEBUG=http2debug=1 kubectl get pods -n demo -l 'e2e notin ($RUN-4)' 2>&1 | grep -c 'http2: Transport encoding header'
5. curl -sS -m 10 'https://$APISERVER_HOST/version?e2e=$RUN-5' 2>&1"
ASKED=1
./openai.sh ask "$PROMPT" "$OUT" > "$OUT/transcript.txt" || ASKED=""
SESSION_ID=$(jq -r '.id // empty' "$OUT/session.json" 2>/dev/null || true)
echo "session $SESSION_ID"

# --------------------------------------------------------------- evidence
# `|| true`: no row is a result to report, not a reason to stop here.
{ kc -n hoop logs deploy/hoop-kube-hoopsidecar --since-time="$SINCE" | grep '^{"kind' || true; } > "$OUT/audit.jsonl"

# ------------------------------------------------------------------ checks
count() { wc -l | tr -d ' '; }
expect() { # expect <got> <want> <description>
    if [[ "$1" == "$2" ]]; then echo "PASS $3"; else echo "FAIL $3 (expected '$2', got '$1')"; fi
}
commands() { jq -c '.[] | select(.type == "command_execution")' "$OUT/items.json"; }
# The output of the shell call whose command holds tag <n>.
output_of() { commands | jq -r --arg t "$RUN-$1" 'select(.command // "" | contains($t)) | .output // ""' | head -c 200000; }
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
    expect "$ASKED" 1 "the session's turn completed within 10 minutes"
    expect "$(commands | jq -c 'select(.command // "" | contains("kubeconfig.sh")) | .exit_code' | tail -1)" 0 \
        "the agent ran kubeconfig.sh, and it succeeded"
    for n in 1 2 3 4 5; do
        expect "$(commands | jq -c --arg t "$RUN-$n" 'select(.command // "" | contains($t))' | count)" 1 \
            "command $n ran once"
    done
    expect "$(output_of 1 | grep -c '^web-')" 2 "kubectl get pods lists the 2 demo pods"
    expect "$(output_of 2 | grep -c "Error from server (Forbidden): $REFUSAL")" 2 \
        "kubectl delete pod is refused for each pod, with the sidecar's message"
    expect "$(kc -n demo get pods -o jsonpath='{.items[*].metadata.uid}')" "$PODS_BEFORE" \
        "the pods still run: no delete reached the API server"
    expect "$(output_of 3 | jq -r '.items[0].data.password' 2>&1)" "[REDACTED:K8S_DATA]" \
        "kubectl get secret -o json shows the password masked"
    expect "$(grep -cE "$SECRET_VALUE|$(printf %s "$SECRET_VALUE" | base64)" "$OUT/items.json")" 0 \
        "no Secret value, plain or base64, reaches the session"
    expect "$(grep -cF -f <(printf '%s\n' "$TOKEN") "$OUT/items.json")" 0 "the real token never appears in the session"
    # The egress proxy's refusal, not any failure: DNS alone fails for every host.
    expect "$(output_of 5 | grep -c 'CONNECT tunnel failed, response 403')" 1 \
        "the egress proxy refuses the direct call to the API server"
    expect "$direct" yes "the same API server answers from outside the sandbox"
    expect "$(rows 1 | jq -r '[.kind, .allowed] | join(" ")')" "statement true" "get pods: one row, admitted"
    expect "$(replies 1)" statement "get pods: one server row for its response"
    expect "$(rows 2 | count)" 1 "delete pod: one row for the list kubectl sends first"
    expect "$(between 2 3 | refused_deletes | count)" 2 \
        "delete pod: one violation row per refused DELETE"
    expect "$(rows 3 | count)" 1 "get secret: one row"
    expect "$(replies 3)" statement,masked "get secret: one server row and one masked row for its response"
    expect "$(rows 5 | count)" 0 "the direct call left no row: it never reached the sidecar"
    expect "$(jq -sr '[.[] | select(.direction == "client") | .http.headers["impersonate-extra-hoop-session"]] | unique | join(",")' \
            "$OUT/audit.jsonl")" "$SESSION_ID" "every client row names the OpenAI session"
    # Not a pass or fail: what the egress proxy negotiated with kubectl.
    headers=$(output_of 4 | tr -dc '0-9')
    echo "INFO kubectl in the sandbox: $([[ "${headers:-0}" -gt 0 ]] && echo "HTTP/2 ($headers headers written)" || echo "HTTP/1.1")"
} | tee "$OUT/result.txt"

echo "evidence: $OUT"
! grep -q '^FAIL' "$OUT/result.txt"
