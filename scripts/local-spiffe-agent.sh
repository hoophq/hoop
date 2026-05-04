#!/usr/bin/env bash
# Phase 2 of 2 — Deploy the SPIFFE-authenticated agent.
#
# Run this after scripts/local-spiffe-kubernetes.sh has finished and you've:
#   1. signed up the local admin via http://localhost:8009
#   2. created the agent record in the UI (default name: local-spiffe)
#   3. run 'hoop login' so the CLI has a cached admin token
#
# This phase uses the 'hoop admin' CLI to look up the agent and create the
# SPIFFE mapping — same flow operators use in production. Then it installs
# the agent helm chart.
#
# Idempotent: re-run any time, helm upgrades in place.

set -euo pipefail

# ---- knobs (must match local-spiffe-kubernetes.sh) ----
TRUST_DOMAIN="${TRUST_DOMAIN:-local.hoop.dev}"
HOOP_NS="${HOOP_NS:-hoop}"

AGENT_NAME="${AGENT_NAME:-local-spiffe}"
SPIFFE_ID="spiffe://${TRUST_DOMAIN}/hoop-agent/${AGENT_NAME}"
AUDIENCE="${AUDIENCE:-http://localhost:8009}"

AGENT_IMAGE_TAG="${AGENT_IMAGE_TAG:-1403.0.0-5a9bd6f}"

# Path to the hoop CLI; HOOP_BIN can be overridden if installed elsewhere.
HOOP_BIN="${HOOP_BIN:-$HOME/.hoop/bin/hoop}"
command -v "$HOOP_BIN" >/dev/null 2>&1 || [ -x "$HOOP_BIN" ] \
  || { echo "hoop CLI not found at $HOOP_BIN — set HOOP_BIN or 'make build-dev-client'"; exit 1; }

REPO_ROOT="${REPO_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"

# Same chart-source resolution as phase 1.
CHART_VERSION="${CHART_VERSION:-}"
if [ -n "$CHART_VERSION" ]; then
  AGENT_CHART="${AGENT_CHART:-oci://ghcr.io/hoophq/helm-charts/hoopagent-chart}"
  HELM_VERSION_FLAG=(--version "$CHART_VERSION")
else
  AGENT_CHART="${AGENT_CHART:-${REPO_ROOT}/deploy/helm-chart/chart/agent}"
  HELM_VERSION_FLAG=()
fi

WORKDIR="${WORKDIR:-${HOME}/.hoop/local-spiffe}"
mkdir -p "$WORKDIR"

log()  { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\n\033[1;33m!!\033[0m  %s\n' "$*"; }

require() {
  for c in "$@"; do
    command -v "$c" >/dev/null || { echo "missing: $c"; exit 1; }
  done
}

require kubectl helm jq

# ---- 0. preflight ----
log "preflight"
if [[ "$AGENT_CHART" != oci://* ]]; then
  [ -d "$AGENT_CHART" ] || { echo "agent chart not at $AGENT_CHART (set REPO_ROOT or CHART_VERSION)"; exit 1; }
fi

# Confirm the gateway is up; phase 1 should have created it.
kubectl -n "$HOOP_NS" get deploy/hoopgateway >/dev/null 2>&1 \
  || { echo "deploy/hoopgateway not found in ns/$HOOP_NS — run scripts/local-spiffe-kubernetes.sh first"; exit 1; }

# Confirm the operator is logged in. 'hoop admin get agents' is the cheapest
# call that exercises both the cached token and the gateway connection — if
# it fails, the rest of the script can't proceed anyway.
if ! "$HOOP_BIN" admin get agents -o json >/dev/null 2>&1; then
  cat <<EOM

================================================================================
'hoop admin' is not authenticated against the local gateway.

Make sure phase 1 has finished, then:

  1. Open http://localhost:8009 and complete the local-auth signup
     (or log in if you already have a user on this gateway).
  2. Create an agent named "${AGENT_NAME}" (mode: standard) in the UI.
  3. Run:    $HOOP_BIN login
  4. Re-run this script.
================================================================================

EOM
  exit 1
fi

# ---- 1. Look up the agent record ----
# 'hoop admin get agents' returns the full list; there is no --name flag, so
# we filter client-side. 'try ... catch empty' tolerates an unexpected JSON
# shape, and 'head -n1' guarantees the pipeline exits 0 even when no agent
# matches (otherwise pipefail would silently kill the script).
log "lookup agent (${AGENT_NAME})"
AGENT_ID=$(
  "$HOOP_BIN" admin get agents -o json 2>/dev/null \
    | jq -r --arg name "$AGENT_NAME" \
        'try (.[] | select(.name == $name) | .id) catch empty' \
    | head -n1
)
if [ -z "$AGENT_ID" ]; then
  cat <<EOM

================================================================================
Agent record "${AGENT_NAME}" not found.

Open http://localhost:8009, create an agent named "${AGENT_NAME}" (mode:
standard) — the UI will show a HOOP_KEY but the SPIFFE flow won't use it
at runtime. Then re-run this script.
================================================================================

EOM
  exit 1
fi
echo "AGENT_ID = $AGENT_ID"

# ---- 2. Create / update the SPIFFE mapping via the CLI ----
# --overwrite makes the call idempotent: it updates the existing row
# (matched by trust_domain + spiffe_id) instead of failing with HTTP 409.
log "create spiffe-mapping (trust_domain=${TRUST_DOMAIN}, spiffe_id=${SPIFFE_ID})"
"$HOOP_BIN" admin create spiffe-mapping \
  --trust-domain "$TRUST_DOMAIN" \
  --spiffe-id "$SPIFFE_ID" \
  --agent-id "$AGENT_ID" \
  --groups agents \
  --overwrite

# ---- 3. SPIFFE agent values + install ----
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

log "agent helm upgrade ($AGENT_CHART${CHART_VERSION:+ @ $CHART_VERSION})"
helm upgrade --install hoopagent "$AGENT_CHART" \
  ${HELM_VERSION_FLAG[@]+"${HELM_VERSION_FLAG[@]}"} \
  -n "$HOOP_NS" \
  -f "$WORKDIR/agent-values.yaml"

log "wait for agent"
kubectl -n "$HOOP_NS" rollout status deploy/hoopagent --timeout=180s

# ---- 4. Verify ----
log "verify"
kubectl -n "$HOOP_NS" get pods
kubectl -n "$HOOP_NS" logs deploy/hoopgateway -c hoopgateway --tail=200 \
  | grep -i 'spiffe' | tail -5 || true
kubectl -n "$HOOP_NS" logs deploy/hoopagent  -c agent --tail=30 \
  | grep -E 'connecting to|connected' || true
"$HOOP_BIN" admin get agents

cat <<EOM

Done. Phase 2 is idempotent — re-run any time.

Useful follow-ups:
  kubectl -n ${HOOP_NS} logs deploy/hoopagent -c spiffe-helper -f
  kubectl -n ${HOOP_NS} logs deploy/hoopagent -c agent -f
  kubectl -n ${HOOP_NS} logs deploy/hoopgateway -c hoopgateway -f

  $HOOP_BIN admin get agents
  $HOOP_BIN admin get spiffe-mappings

EOM
