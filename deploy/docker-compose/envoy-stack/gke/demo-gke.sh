#!/usr/bin/env bash
#
# Walks the GKE Connect Gateway overlay of the envoy-stack.
#
#   kubectl ──TLS h2──> envoy :443 (MITM) ──h2c──> hoop-inspect ──TLS──> fake-google ──> k3s
#
# Prereqs, from the envoy-stack directory:
#   ./run.sh --rebuild
#   docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml up -d --build --wait
#
# Nothing here touches Google: fake-google answers for both
# connectgateway.googleapis.com and oauth2.googleapis.com inside the compose
# network.

set -uo pipefail
cd "$(dirname "$0")/.."

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }

COMPOSE="docker compose --progress quiet -f docker-compose.yml -f gke/docker-compose.gke.yml"
KUBECTL="$COMPOSE exec -T client kubectl"
CURL="$COMPOSE exec -T client curl -sS --cacert /etc/gke-pki/ca.crt"
GW=https://connectgateway.googleapis.com/v1/projects/123456789012/locations/global/gkeMemberships/demo

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not up. From the envoy-stack directory:" >&2
    echo "  docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml up -d --build --wait" >&2
    exit 1
fi

DEMO_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1

h "TRANSPARENT / kubectl keeps the real Connect Gateway URL"
note "The kubeconfig names $GW."
note "Compose resolves the host to Envoy, which intercepts TLS with the customer"
note "CA and sends h2c to the sidecar. Nothing on the client knows a proxy exists."
$KUBECTL get pods -v=6 2>&1 | grep -E 'GET https|^NAME|^demo' | sed -E 's/^I[0-9]+ [0-9:.]+ +[0-9]+ [a-z_]+\.go:[0-9]+\] /  /; s/^(NAME|demo)/  \1/'

h "IDENTITY / the caller comes from kubectl's own Google bearer"
note "google_identity checks the token with tokeninfo (fake-google answering as"
note "oauth2.googleapis.com, trusted through trust.ca_file). Envoy runs no"
note "ext_authz and sets no header. bob gets through the sidecar as bob, and the"
note "cluster's RBAC refuses him, as Connect Gateway would:"
$KUBECTL --context bob get pods 2>&1 | sed 's/^/  /'
note ""
note "A token Google does not know is refused by the sidecar before any upstream:"
$CURL -H 'Authorization: Bearer forged' "$GW/api/v1/namespaces/default/pods" -w '  [%{http_code}]\n' 2>&1 | sed 's/^/  /'

h "RULES / plain Kubernetes paths, no gateway prefix"
note "no-secret-contents names /api/v1/namespaces/*/secrets/**. The lane removes"
note "/v1/projects/*/locations/*/gkeMemberships/* before rules see the path."
note "Listing is fine:"
$KUBECTL get secrets 2>&1 | sed 's/^/  /'
note ""
note "Reading one is not:"
$KUBECTL get secret db-credentials -o yaml 2>&1 | sed 's/^/  /'

h "MASKING / through HTTP/2"
note "The ConfigMap's data comes back redacted; the table view is untouched."
$KUBECTL get configmap customers 2>&1 | sed 's/^/  /'
$KUBECTL get configmap customers -o jsonpath='{.data.ada}{"\n"}' 2>&1 | sed 's/^/  /'

h "WEBSOCKET / kubectl exec over HTTP/1.1"
note "Upgrades cannot ride HTTP/2, so Envoy routes them to the same sidecar port"
note "as HTTP/1.1; the lane serves both."
$KUBECTL exec demo -- sh -c 'echo hello from $(hostname)' 2>&1 | sed 's/^/  /'

h "LOOP / a MITM that also intercepts the sidecar's own egress"
note "Envoy :8449 routes to a lane whose upstream is :8449 again. Each pass adds"
note "the sidecar's Via; the second pass sees its own and refuses:"
$COMPOSE exec -T client curl -sS --cacert /etc/gke-pki/ca.crt \
    --connect-to connectgateway.googleapis.com:443:envoy:8449 \
    -H 'Authorization: Bearer tok-alice' "$GW/api/v1/namespaces/default/pods" -w '\n  [%{http_code}]\n' 2>&1 | sed 's/^/  /'

h "AUDIT / what hoop-inspect recorded"
sleep 1
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null \
    | grep -o '{"kind":"\(session_start\|statement\|violation\)".*' \
    | python3 -c '
import json, sys
for line in sys.stdin:
    try: e = json.loads(line)
    except ValueError: continue
    if e.get("connection") != "gke": continue
    meta = e.get("metadata") or {}
    print("  %-13s %s  %-18s %-7s %s" % (e["kind"], e["session_id"][:8], e["principal"],
          meta.get("http.proto", ""), (e.get("statement") or "")[:90] + ("  <" + e["rule"] + ">" if e.get("rule") else "")))
'
note ""
note "Bearer tokens in the trail (expect 0):"
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null | grep -c 'tok-alice\|tok-bob\|forged' | sed 's/^/  /'
note ""
note "What fake-google saw (Via from the sidecar, HTTP/1.1 upstream hop):"
$COMPOSE logs fake-google --since "$DEMO_START" 2>/dev/null | grep -E 'gateway|tokeninfo' | sed -E 's/^[^|]*\| //; s/^/  /' | tail -8
