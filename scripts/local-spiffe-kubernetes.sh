#!/usr/bin/env bash
# Bring up Hoop gateway + SPIFFE agent on local k3s/colima.
# Idempotent: re-run after edits, helm upgrades in place.

set -euo pipefail

# ---- knobs ----
TRUST_DOMAIN="${TRUST_DOMAIN:-local.hoop.dev}"
HOOP_NS="${HOOP_NS:-hoop}"
SPIRE_MGMT_NS="${SPIRE_MGMT_NS:-spire-mgmt}"
SPIRE_SERVER_NS="${SPIRE_SERVER_NS:-spire-server}"
SPIRE_SYSTEM_NS="${SPIRE_SYSTEM_NS:-spire-system}"

AGENT_NAME="${AGENT_NAME:-local-spiffe}"
SPIFFE_ID="spiffe://${TRUST_DOMAIN}/hoop-agent/${AGENT_NAME}"
AUDIENCE="${AUDIENCE:-http://localhost:8009}"

# Pin both images to a known-good build.
# 1403.0.0-668b288  -> includes the GORM fix for agent_spiffe_mappings.
# 1403.0.0-5a9bd6f  -> also includes HOOP_GRPC_INSECURE in the agent.
GATEWAY_IMAGE_TAG="${GATEWAY_IMAGE_TAG:-1403.0.0-5a9bd6f}"
AGENT_IMAGE_TAG="${AGENT_IMAGE_TAG:-1403.0.0-5a9bd6f}"

REPO_ROOT="${REPO_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
GATEWAY_CHART="${REPO_ROOT}/deploy/helm-chart/chart/gateway"
AGENT_CHART="${REPO_ROOT}/deploy/helm-chart/chart/agent"

WORKDIR="${WORKDIR:-${HOME}/.hoop/local-spiffe}"
mkdir -p "$WORKDIR"

log()  { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\n\033[1;33m!!\033[0m  %s\n' "$*"; }

require() {
  for c in "$@"; do
    command -v "$c" >/dev/null || { echo "missing: $c"; exit 1; }
  done
}

require kubectl helm jq colima

# ---- 0. preflight ----
log "preflight"
kubectl get nodes >/dev/null
[ -d "$GATEWAY_CHART" ] || { echo "gateway chart not at $GATEWAY_CHART (set REPO_ROOT)"; exit 1; }
[ -d "$AGENT_CHART"   ] || { echo "agent chart not at $AGENT_CHART"; exit 1; }

# ---- 1. namespaces ----
log "namespaces"
for ns in "$HOOP_NS" "$SPIRE_MGMT_NS" "$SPIRE_SERVER_NS" "$SPIRE_SYSTEM_NS"; do
  kubectl get ns "$ns" >/dev/null 2>&1 || kubectl create ns "$ns"
done

# ---- 2. SPIRE (hostPath, no CSI) ----
log "SPIRE chart repo + CRDs"
helm repo add spiffe https://spiffe.github.io/helm-charts-hardened/ >/dev/null 2>&1 || true
helm repo update >/dev/null

helm upgrade --install -n "$SPIRE_MGMT_NS" \
  spire-crds spiffe/spire-crds >/dev/null

cat > "$WORKDIR/spire-values.yaml" <<EOF
global:
  spire:
    clusterName: local-hoop
    trustDomain: ${TRUST_DOMAIN}
    caSubject:
      country: US
      organization: hoop-local
      commonName: ${TRUST_DOMAIN}
    recommendations:
      enabled: true
    bundleConfigMap: spire-bundle

spire-agent:
  socketPath: /run/spire/agent-sockets/api.sock
  hostSocket:
    enabled: true
    path: /run/spire/agent-sockets

spiffe-csi-driver:
  enabled: false
EOF

log "SPIRE install/upgrade"
helm upgrade --install -n "$SPIRE_MGMT_NS" \
  spire spiffe/spire \
  -f "$WORKDIR/spire-values.yaml"

log "wait for spire-server + spire-agent"
kubectl -n "$SPIRE_SERVER_NS" rollout status statefulset/spire-server --timeout=180s
kubectl -n "$SPIRE_SYSTEM_NS" rollout status daemonset/spire-agent  --timeout=180s

log "verify socket on the node"
colima ssh -- ls -la /run/spire/agent-sockets/

# ---- 3. SPIRE registration entry (waits for the agent to attest) ----
log "SPIRE registration entry"
SPIRE_SERVER_POD=spire-server-0

# Wait for the spire-agent to register itself before we can use its SPIFFE ID
# as parentID. Retry up to 30s.
PARENT_ID=""
for _ in $(seq 1 30); do
  PARENT_ID=$(
    kubectl -n "$SPIRE_SERVER_NS" exec "$SPIRE_SERVER_POD" -c spire-server -- \
      /opt/spire/bin/spire-server agent list 2>/dev/null \
      | awk '/SPIFFE ID/ {print $4; exit}'
  ) || true
  [ -n "$PARENT_ID" ] && break
  sleep 1
done
[ -n "$PARENT_ID" ] || { echo "spire-agent never attested"; exit 1; }
echo "parentID = $PARENT_ID"

# Create entry only if it doesn't already exist (idempotent).
if ! kubectl -n "$SPIRE_SERVER_NS" exec "$SPIRE_SERVER_POD" -c spire-server -- \
       /opt/spire/bin/spire-server entry show -spiffeID "$SPIFFE_ID" \
       2>/dev/null | grep -q "$SPIFFE_ID"; then
  kubectl -n "$SPIRE_SERVER_NS" exec "$SPIRE_SERVER_POD" -c spire-server -- \
    /opt/spire/bin/spire-server entry create \
      -spiffeID  "$SPIFFE_ID" \
      -parentID  "$PARENT_ID" \
      -selector  "k8s:ns:${HOOP_NS}" \
      -selector  "k8s:sa:hoopagent" \
      -jwtSVIDTTL 300
else
  echo "entry already exists, skipping"
fi

# ---- 4. Trust bundle (JWKS) ----
log "export SPIRE trust bundle"
kubectl -n "$SPIRE_SERVER_NS" exec "$SPIRE_SERVER_POD" -c spire-server -- \
  /opt/spire/bin/spire-server bundle show -format spiffe \
  > "$WORKDIR/spire-bundle.jwks"
[ -s "$WORKDIR/spire-bundle.jwks" ] || { echo "empty bundle"; exit 1; }

# ---- 5. Gateway values + install ----
log "gateway values"
# Use --set-file for the JWKS so we don't have to embed multi-line JSON in YAML.
cat > "$WORKDIR/gateway-values.yaml" <<EOF
image:
  gw:
    repository: hoophq/hoop
    tag: ${GATEWAY_IMAGE_TAG}
    pullPolicy: IfNotPresent

config:
  POSTGRES_DB_URI: 'postgres://root:default-pwd@hoopgateway-pg:5432/postgres?sslmode=disable'
  API_URL: 'http://localhost:8009'
  GRPC_URL: 'grpc://localhost:8010'
  AUTH_METHOD: 'local'
  LOG_LEVEL: 'debug'
  LOG_ENCODING: 'console'

  HOOP_SPIFFE_MODE: 'enforce'
  HOOP_SPIFFE_TRUST_DOMAIN: '${TRUST_DOMAIN}'
  HOOP_SPIFFE_AUDIENCE: '${AUDIENCE}'
  HOOP_SPIFFE_REFRESH_PERIOD: '5m'

postgres:
  enabled: true

defaultAgent:
  enabled: true
EOF

log "gateway helm upgrade"
helm upgrade --install hoop "$GATEWAY_CHART" \
  -n "$HOOP_NS" \
  -f "$WORKDIR/gateway-values.yaml" \
  --set-file config.HOOP_SPIFFE_BUNDLE_JWKS="$WORKDIR/spire-bundle.jwks"

log "wait for gateway"
kubectl -n "$HOOP_NS" rollout status deploy/hoopgateway --timeout=180s

# ---- 6. Port-forward (background, idempotent) ----
log "port-forward 8009 + 8010"
pkill -f "port-forward.*hoopgateway" 2>/dev/null || true
sleep 1
kubectl -n "$HOOP_NS" port-forward svc/hoopgateway 8009:8009 \
  >"$WORKDIR/pf-8009.log" 2>&1 &
kubectl -n "$HOOP_NS" port-forward svc/hoopgateway 8010:8010 \
  >"$WORKDIR/pf-8010.log" 2>&1 &
sleep 3

# ---- 7. MANUAL: admin signup ----
if ! curl -fsS http://localhost:8009/api/userinfo \
      -H "Authorization: Bearer ${HOOP_TOKEN:-none}" >/dev/null 2>&1; then
  cat <<EOM

================================================================================
MANUAL STEP — sign up the local admin (one time only):

  1. open http://localhost:8009 in a browser
  2. complete the local-auth signup (the first user is admin)
  3. run:    hoop login    (it'll target http://localhost:8009)
  4. re-run this script — it will skip past this step on the next run.
================================================================================

EOM
  exit 0
fi

# ---- 8. Hoop agent record + SPIFFE mapping ----
log "hoop agent + spiffe-mapping"
HOOP_BIN="${HOOP_BIN:-hoop}"

if ! "$HOOP_BIN" admin get agents --name "$AGENT_NAME" -o json 2>/dev/null \
      | jq -e '.id' >/dev/null; then
  "$HOOP_BIN" admin create agent "$AGENT_NAME" --mode standard
fi

AGENT_ID=$("$HOOP_BIN" admin get agents --name "$AGENT_NAME" -o json | jq -r '.id')
[ -n "$AGENT_ID" ] && [ "$AGENT_ID" != "null" ] || { echo "AGENT_ID not resolved"; exit 1; }

"$HOOP_BIN" admin create spiffe-mapping \
  --trust-domain "$TRUST_DOMAIN" \
  --spiffe-id "$SPIFFE_ID" \
  --agent-id "$AGENT_ID" \
  --groups agents \
  --overwrite

# ---- 9. SPIFFE agent values + install ----
log "agent values"
cat > "$WORKDIR/agent-values.yaml" <<EOF
image:
  repository: hoophq/hoopdev
  tag: ${AGENT_IMAGE_TAG}
  pullPolicy: IfNotPresent

replicaCount: 1

serviceAccount:
  create: true

spiffe:
  enabled: true
  grpcHost: hoopgateway:8010
  grpcSkipVerify: true
  grpcInsecure: true     # gateway 8010 is plaintext locally (no TLS_KEY/TLS_CERT)
  name: ${AGENT_NAME}

  workloadAPI:
    type: hostPath
    hostPath: /run/spire/agent-sockets

  spiffeHelper:
    agentAddress: /spiffe-workload-api/api.sock
    audience: ${AUDIENCE}

deploymentStrategy:
  type: Recreate
EOF

log "agent helm upgrade"
helm upgrade --install hoopagent "$AGENT_CHART" \
  -n "$HOOP_NS" \
  -f "$WORKDIR/agent-values.yaml"

log "wait for agent"
kubectl -n "$HOOP_NS" rollout status deploy/hoopagent --timeout=180s

# ---- 10. Verify ----
log "verify"
kubectl -n "$HOOP_NS" get pods
kubectl -n "$HOOP_NS" logs deploy/hoopgateway -c hoopgateway --tail=200 \
  | grep -i 'spiffe' | tail -5 || true
kubectl -n "$HOOP_NS" logs deploy/hoopagent  -c agent --tail=30 \
  | grep -E 'connecting to|connected' || true
"$HOOP_BIN" admin get agents

cat <<EOM

Done. Re-run this script any time; everything is idempotent.

Useful follow-ups:
  kubectl -n $HOOP_NS logs deploy/hoopagent -c spiffe-helper -f
  kubectl -n $HOOP_NS logs deploy/hoopgateway -c hoopgateway -f
  $HOOP_BIN admin get agents
  $HOOP_BIN admin get spiffe-mappings

EOM
