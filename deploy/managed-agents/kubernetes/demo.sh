#!/usr/bin/env bash
#
# Checks every claim of this template on this machine, with no Anthropic
# call. kubectl reaches the lane on https://localhost:8443 with the agent's
# token, as the sandbox reaches it through the egress proxy.
#
#   1. transport  the lane serves kubectl over TLS with ALPN h2
#   2. kubectl    get works, delete is refused, Secret values are masked
#   3. audit      one client row per request; a refused one never runs
#   4. limits     what README.md "Limits" states still holds
#
# Prereqs: ./run.sh (or --tunnel), jq, curl.

# No -e: a failed check records the failure, and the next checks still run.
set -uo pipefail
cd "$(dirname "$0")" || exit 1

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
pass() { printf '\033[32m  PASS\033[0m %s\n' "$*"; }
fail() { printf '\033[31m  FAIL\033[0m %s\n' "$*"; FAILED=1; }
FAILED=0

# `[[ ... ]] && pass || fail` also runs fail when pass returns nonzero.
expect() { # expect <got> <want> <description>
    if [[ "$1" == "$2" ]]; then pass "$3"; else fail "$3 (expected '$2', got '$1')"; fi
}
contains() { # contains <text> <substring> <description>
    if [[ "$1" == *"$2"* ]]; then pass "$3"; else fail "$3 (no '$2' in: ${1:0:160})"; fi
}
lacks() { # lacks <text> <regex> <description>
    if grep -qE "$2" <<<"$1"; then fail "$3 (found /$2/)"; else pass "$3"; fi
}
count() { wc -l | tr -d ' '; }

for t in jq curl kubectl; do command -v "$t" >/dev/null || { echo "$t is required" >&2; exit 1; }; done
[[ -f .env ]] || { echo "No .env. Run ./run.sh first." >&2; exit 1; }
CTX=$(sed -n 's/^CONTEXT=//p' .env)
ADMIN_KUBECONFIG=${KUBECONFIG:-$HOME/.kube/config}
# The cluster directly, as its admin. Bare kubectl, further down, is the agent.
kc() { KUBECONFIG=$ADMIN_KUBECONFIG kubectl --context "$CTX" "$@"; }
kc -n hoop get deploy/hoop-kube-hoopsidecar >/dev/null 2>&1 \
    || { echo "The sidecar is not installed. Run ./run.sh first." >&2; exit 1; }

RUN=r$(date +%s)
SECRET_VALUE='hunter2-demo-value'
# The value as it sits in `data` (base64) and in the annotation (plain).
LEAK="$SECRET_VALUE|$(printf %s "$SECRET_VALUE" | base64)"
REFUSAL='this agent may not delete cluster objects'
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# The agent's kubeconfig: the sidecar's address and the agent's token. The
# lane's certificate is self-signed here, so it is the CA; the sandbox trusts
# the egress proxy's instead. Written by the shell, not by `kubectl config`,
# so the real token is in no process's argv.
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
TOKEN=$(kc -n hoop get secret claude-agent-token -o jsonpath='{.data.token}' | base64 -d)
kc -n hoop get secret hoop-sidecar-tls -o jsonpath='{.data.tls\.crt}' | base64 -d > "$WORK/ca.crt"
(umask 077
 printf 'Authorization: Bearer %s\n' "$TOKEN" > "$WORK/auth"
 cat > "$WORK/kubeconfig" <<EOF
apiVersion: v1
kind: Config
clusters: [{name: hoop, cluster: {server: 'https://localhost:8443', certificate-authority: '$WORK/ca.crt'}}]
users: [{name: agent, user: {token: '$TOKEN'}}]
contexts: [{name: hoop, context: {cluster: hoop, user: agent, namespace: demo}}]
current-context: hoop
EOF
)
export KUBECONFIG=$WORK/kubeconfig
api() { curl -sS --cacert "$WORK/ca.crt" -H @"$WORK/auth" "$@"; }
audit() { kc -n hoop logs deploy/hoop-kube-hoopsidecar --since-time="$SINCE" | grep '^{"kind'; }
# The client row of each request whose statement holds <text>.
rows() { audit | jq -c --arg t "$1" 'select(.direction == "client" and (.statement // "" | contains($t)))'; }
# The rows logged after the request tagged <a> and before the one tagged <b>.
# Log order, so a run before or after this one adds none.
between() { audit | jq -sc --arg a "$1" --arg b "$2" '
    def at($t): map(.direction == "client" and (.statement // "" | contains($t))) | index(true);
    at($a) as $i | at($b) as $j | if $i and $j then .[$i + 1:$j][] else empty end'; }
refused_deletes() { jq -c 'select(.kind == "violation" and .rule == "no-delete" and (.statement | startswith("DELETE /api/v1/namespaces/demo/pods/web-")))'; }

h "1. transport: TLS with ALPN h2"
expect "$(api -o /dev/null -w '%{http_version}' --http2 https://localhost:8443/version)" 2 \
    "curl offered h2 and http/1.1; the lane chose h2"
expect "$(api -o /dev/null -w '%{http_version}' --http1.1 https://localhost:8443/version)" 1.1 \
    "a client that offers only http/1.1 is served too"
# kubectl is Go: under this flag its HTTP/2 transport logs each header it
# writes, and an HTTP/1.1 connection logs none.
contains "$(GODEBUG=http2debug=1 kubectl get --raw /version 2>&1 >/dev/null)" "http2: Transport encoding header" \
    "kubectl talks HTTP/2 to the lane"

h "2. kubectl through the lane"
pods_before=$(kc -n demo get pods -o jsonpath='{.items[*].metadata.uid}')
got=$(kubectl get pods -l "e2e notin ($RUN-1)" 2>&1)
contains "$got" "web-" "kubectl get pods lists the demo pods"
got=$(kubectl delete pod -l "app=web,e2e notin ($RUN-2)" --wait=false 2>&1)
expect "$(grep -c "Error from server (Forbidden): $REFUSAL" <<<"$got")" 2 \
    "kubectl delete pod is refused for each pod, with the sidecar's message"
expect "$(api -o /dev/null -w '%{http_code}' -X DELETE "https://localhost:8443/api/v1/namespaces/demo/pods?e2e=$RUN-3")" 403 \
    "a deletecollection is refused too"
expect "$(kc -n demo get pods -o jsonpath='{.items[*].metadata.uid}')" "$pods_before" \
    "the pods still run: no delete reached the API server"
got=$(kubectl get secret -l "e2e notin ($RUN-4)" -o yaml 2>&1)
contains "$got" "password: '[REDACTED:K8S_DATA]'" "kubectl get secret -o yaml masks each value"
contains "$got" "last-applied-configuration: '[REDACTED:K8S_DATA]'" \
    "the last-applied annotation, a second copy, is masked too"
lacks "$got" "$LEAK" "no Secret value, plain or base64, reaches kubectl"
contains "$(kubectl get secrets 2>&1)" "db-credentials" "kubectl get secrets (the table view) lists names"
got=$(api -m 3 "https://localhost:8443/api/v1/namespaces/demo/secrets?watch=true&e2e=$RUN-5" 2>/dev/null)
contains "$got" '"type":"ADDED"' "a watch on Secrets streams its events"
lacks "$got" "$LEAK" "the watch stream is masked event by event"
got=$(kubectl scale deploy/web --replicas=3 2>&1; kubectl scale deploy/web --replicas=2 2>&1)
expect "$(grep -c 'scaled' <<<"$got")" 2 "kubectl scale (a PATCH) passes: the agent can manage, not delete"

h "3. audit: one client row per request"
sleep 1 # the sidecar writes the row before it answers; give the log a moment
expect "$(rows "$RUN-1" | jq -r '[.kind, .operation, .allowed] | join(" ")')" "statement get true" \
    "get pods: one row, admitted"
expect "$(rows "$RUN-2" | count)" 1 "delete pod: one row for the list kubectl sends first"
expect "$(between "$RUN-2" "$RUN-3" | refused_deletes | count)" 2 \
    "delete pod: one violation row per refused DELETE"
expect "$(rows "$RUN-3" | jq -r '[.kind, .rule] | join(" ")')" "violation no-delete" \
    "deletecollection: one violation row, rule no-delete"
expect "$(rows "$RUN-4" | count)" 1 "get secret: one row"
expect "$(rows "$RUN-1" | jq -r '.http.headers["kubectl-command"]')" "kubectl get" \
    "a row names the kubectl subcommand"
expect "$(rows "$RUN-1" | jq -r '.principal')" "anonymous" \
    "a row names no caller: kubectl cannot send the sidecar a caller token"

h "4. limits stated in README.md"
# These pass while the limit exists. A FAIL here means the README is wrong.
for accept in application/yaml application/vnd.kubernetes.protobuf; do
    expect "$(api "https://localhost:8443/api/v1/namespaces/demo/secrets/db-credentials" \
            -H "Accept: $accept" | LC_ALL=C grep -a -c -E "$LEAK")" 2 \
        "a read as $accept returns the value unmasked"
done
contains "$(kubectl get secrets -n hoop 2>&1)" "cannot list resource \"secrets\"" \
    "rbac.yaml grants Secret read in demo only"
contains "$(kubectl set env deploy/web LEAK=1 2>&1)" "cannot patch resource \"deployments\"" \
    "rbac.yaml refuses a change to a pod spec, the way to print a Secret in a log"
expect "$(kubectl get configmap app-config -o jsonpath='{.data.LOG_LEVEL}' 2>&1)" "[REDACTED:K8S_DATA]" \
    "a ConfigMap's data is masked as well"

echo
if [[ "$FAILED" == 0 ]]; then echo "ALL PASSED"; else echo "FAILED"; exit 1; fi
