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
# The lane inherits the process's one guardrail rule. Plain requests and the
# WebSocket that kubectl exec opens cross the same listener.

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
note "to the apiserver, verified against the cluster CA."
$KUBECTL get pods -o wide 2>&1 | sed 's/^/  /'

h "HTTP / denied -- the one guardrail rule"
note "no-cpf-in-query scans the request line. A resource name carrying a"
note "taxpayer id is refused with the relay's 403; the apiserver never saw"
note "the GET. Same rule, same message as the pgwire DELETE in ../demo.sh."
$KUBECTL get configmap 111.444.777-35 2>&1 | sed 's/^/  /'

h "HTTP / the response, not masked"
note "The ConfigMap carries the customers fixture. The apiserver answers with"
note "Transfer-Encoding: chunked, always, and http masking substitutes bytes"
note "only under a Content-Length it can correct. The emails come back in the"
note "clear, and config-kubernetes.yaml switches the mask rule off on this"
note "lane rather than carry one that never fires."
$KUBECTL get configmap customers -o jsonpath='{.data.ada}{"\n"}{.data.grace}{"\n"}{.data.alan}{"\n"}' 2>&1 | sed 's/^/  /'

# -------------------------------------------------------------- websocket
h "WEBSOCKET / kubectl exec"
note "kubectl 1.30+ opens exec over WebSocket: GET .../pods/demo/exec with"
note "Upgrade: websocket, answered 101. Envoy's upgrade_configs lets it"
note "through as a connection; the relay records the GET, the 101 and the"
note "subprotocol the apiserver selected. -v=6 prints the round trip:"
$KUBECTL exec demo -v=6 -- sh -c 'echo hello from $(hostname)' 2>&1 \
    | grep -E 'round_trippers.*exec|^hello' | sed -E 's/^I[0-9]+ [0-9:.]+ +[0-9]+ round_trippers.go:[0-9]+\] /  /; s/^hello/  hello/'

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
note "Every request line above, the 101, and -- with a WebSocket-aware http"
note "codec -- one row per message on the exec connection. Authorization is"
note "never allowlisted, so no bearer token is in this trail."
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null | ./sidecar/read-audit.py
note ""
note "The 101 as recorded, with the headers the lane allowlists:"
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null \
    | grep -o '"statement":"101 Switching Protocols".*"headers":{[^}]*}' | head -1 | sed 's/^/  /'

h "Summary"
cat <<'EOF'
  One apiserver, one http lane, two request shapes. OPA gated reachability
  on the bearer token; the relay refused a taxpayer id on the request line
  and recorded every request, the WebSocket handshake included. kubectl
  exec ran end to end through Envoy, OPA and the relay.
EOF
