#!/usr/bin/env bash
#
# Walks the Kubernetes lane of the envoy-stack.
#
#   kubectl ──TLS──> envoy:8447 ──OPA──> hoop-inspect ──TLS──> k3s:6443
#
# Prereqs, from the envoy-stack directory:
#   ./run.sh
#   docker compose -f docker-compose.yml -f kubernetes/docker-compose.kubernetes.yml up -d --wait
#
# The lane carries the process's one guardrail (an http_header rule on
# Secrets) and its one mask rule (the `data` key of any JSON response). Plain
# requests and the WebSocket that kubectl exec opens cross the same listener.

set -uo pipefail
cd "$(dirname "$0")/.."

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }

COMPOSE="docker compose --progress quiet -f docker-compose.yml -f kubernetes/docker-compose.kubernetes.yml"
# KUBECONFIG is set on the container: envoy:8447, Envoy's cert as the CA,
# alice's token as the default context.
KUBECTL="$COMPOSE exec -T client kubectl"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Bring the overlay up first:" >&2
    echo "  ./run.sh && $COMPOSE up -d --wait" >&2
    exit 1
fi

DEMO_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1

# ------------------------------------------------------------------ tier 1
h "TIER 1 :8447 / OPA's fat gate, keyed on the bearer token"
note "kubectl cannot add a header, so on this listener ../opa/authz.rego reads"
note "the identity off Authorization. bob's token resolves to bob, who has no"
note "grant on \"kubernetes\". hoop-inspect never saw the request; kubectl"
note "prints OPA's body as the server's error."
$KUBECTL --context bob get pods 2>&1 | sed 's/^/  /'
note ""
note "No token at all is a 401, before the apiserver could say the same:"
$COMPOSE exec -T client curl -sk https://envoy:8447/api -w ' [%{http_code}]\n' 2>&1 | sed 's/^/  /'

# ------------------------------------------------------------------- http
h "HTTP / alice reads the cluster"
note "A plain GET, TLS terminated by Envoy, HTTP/1.1 to the relay, TLS again"
note "to the apiserver, verified against the cluster CA. No body anywhere:"
note "the path is the operation, and the headers the lane allowlists say the"
note "rest (Accept, Kubectl-Command)."
$KUBECTL get pods -o wide 2>&1 | sed 's/^/  /'

h "HTTP / listing secrets is fine"
note "kubectl get secrets asks for the table view: Accept: application/json;"
note "as=Table;v=v1;g=meta.k8s.io. The guardrail reads that header and does"
note "not match; names and types come back, contents never do."
$KUBECTL get secrets 2>&1 | sed 's/^/  /'

h "HTTP / denied -- reading one is not"
note "The same GET on one secret with -o yaml carries Accept: application/json,"
note "the request for the object itself. no-secret-contents, the process's one"
note "guardrail, is an http_header rule on GET /api/v1/namespaces/*/secrets/**"
note "that refuses every Accept but the table view. The apiserver never saw"
note "the request, and neither did a model."
$KUBECTL get secret db-credentials -o yaml 2>&1 | sed 's/^/  /'

h "HTTP / the response, masked by JSON key"
note "The ConfigMap carries the customers fixture. The apiserver answers with"
note "Transfer-Encoding: chunked; the WebSocket-aware http codec walks the JSON"
note "value by value and hands each to the masker under its key path (data.ada),"
note "so k8s-data, a columns: [data] rule, redacts every value under data and"
note "re-chunks what it rewrote. kubectl's table view is untouched:"
$KUBECTL get configmap customers 2>&1 | sed 's/^/  /'
note ""
note "The object view is not:"
$KUBECTL get configmap customers -o jsonpath='{.data.ada}{"\n"}{.data.grace}{"\n"}{.data.alan}{"\n"}' 2>&1 | sed 's/^/  /'

# -------------------------------------------------------------- websocket
h "WEBSOCKET / kubectl exec"
note "kubectl 1.30+ opens exec over WebSocket: GET .../pods/demo/exec with"
note "Upgrade: websocket, answered 101. Envoy's upgrade_configs lets it"
note "through as a connection; the relay records the GET, the 101, and one"
note "row per message after it. -v=6 prints the round trip:"
$KUBECTL exec demo -v=6 -- sh -c 'echo hello from $(hostname)' 2>&1 \
    | grep -E 'round_trippers.*exec|^hello' | sed -E 's/^I[0-9]+ [0-9:.]+ +[0-9]+ round_trippers.go:[0-9]+\] /  /; s/^hello/  hello/'

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
note "Every request line above with its allowlisted headers, the 403 with the"
note "rule that produced it, the masked event, the 101, and one ws_message row"
note "per frame on the exec connection. Authorization is never allowlisted, so"
note "no bearer token is in this trail."
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null | ./sidecar/read-audit.py
note ""
note "The refused GET as recorded: principal, rule, and the headers the rule read:"
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null \
    | grep -o '{"kind":"violation".*"rule":"no-secret-contents".*}' | head -1 \
    | python3 -c 'import json,sys; e=json.loads(sys.stdin.readline()); print(" ", e["principal"], e["rule"], json.dumps(e["http"]["headers"]))'
note ""
note "The exec session's frames:"
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null \
    | grep -o '"statement":"WS [a-z]* [^"]*exec[^"]*"' | sort | uniq -c | sed 's/^/  /'

h "Summary"
cat <<'EOF'
  One apiserver, one http lane, two request shapes, no request body anywhere.
  OPA gated reachability on the bearer token; the relay read the Accept header
  to let a secret be listed and refuse it being read, redacted every `data`
  value in a chunked JSON response, and recorded every request, the WebSocket
  handshake and each frame after it. kubectl exec ran end to end through
  Envoy, OPA and the relay.
EOF
