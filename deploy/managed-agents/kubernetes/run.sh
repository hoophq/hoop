#!/usr/bin/env bash
#
# A local k3d cluster with this template installed: the chart from this
# repository with values.yaml unchanged, a self-signed certificate in place
# of a public one, rbac.yaml, and demo objects. Writes .env.
#
# Usage:
#   ./run.sh            the cluster; then ./demo.sh
#   ./run.sh --tunnel   also two Cloudflare quick tunnels; then ./e2e.sh
#   ./run.sh down       remove the tunnels and the cluster
#
# Every kubectl and helm call names the k3d context, so no other cluster is
# touched.

set -euo pipefail
cd "$(dirname "$0")"

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

CLUSTER=hoop-kube
CTX=k3d-$CLUSTER
K3S_IMAGE=rancher/k3s:v1.35.5-k3s1
CLOUDFLARED=cloudflare/cloudflared:2026.10.0@sha256:9b49eed8f62806d5d45ddf59ecefb5710429598ea6d3fcccd2af938f621b2b07
TUNNELS=(hoop-kube-tunnel-sidecar hoop-kube-tunnel-apiserver)
kc() { kubectl --context "$CTX" "$@"; }

if [[ "${1:-}" == "down" ]]; then
    docker rm -f "${TUNNELS[@]}" >/dev/null 2>&1 || true
    k3d cluster delete "$CLUSTER"
    rm -f .env
    exit 0
fi

case "${1:-}" in
    "")       TUNNEL="" ;;
    --tunnel) TUNNEL=1 ;;
    *)        die "unknown argument: $1 (see the header of $0)" ;;
esac
for t in k3d kubectl helm docker openssl; do command -v "$t" >/dev/null || die "missing required tool: $t"; done

c_step "Starting k3d cluster $CLUSTER"
if ! k3d cluster list "$CLUSTER" >/dev/null 2>&1; then
    # The k3d load balancer publishes the lane's Service on 8443 and the API
    # server on 6550, both on loopback only.
    k3d cluster create "$CLUSTER" --image "$K3S_IMAGE" --wait \
        --api-port 127.0.0.1:6550 -p '127.0.0.1:8443:443@loadbalancer' >/dev/null
fi
c_ok "context $CTX"

c_step "Installing the demo objects, rbac.yaml and the lane certificate"
kc create namespace hoop --dry-run=client -o yaml | kc apply -f - >/dev/null
# `kubectl apply` writes the last-applied annotation, a second copy of each
# Secret value that the mask rule has to cover.
kc apply -f - >/dev/null <<'EOF'
apiVersion: v1
kind: Namespace
metadata: {name: demo}
---
apiVersion: v1
kind: Secret
metadata: {name: db-credentials, namespace: demo}
stringData: {username: app, password: hunter2-demo-value}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: app-config, namespace: demo}
data: {LOG_LEVEL: info}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: demo}
spec:
  replicas: 2
  selector: {matchLabels: {app: web}}
  template:
    metadata: {labels: {app: web}}
    spec:
      containers:
        - {name: pause, image: 'registry.k8s.io/pause:3.10'}
EOF
kc apply -f rbac.yaml >/dev/null # its Role lives in demo
if ! kc -n hoop get secret hoop-sidecar-tls >/dev/null 2>&1; then
    tmp=$(mktemp -d)
    openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj /CN=localhost \
        -addext subjectAltName=DNS:localhost,IP:127.0.0.1 \
        -keyout "$tmp/tls.key" -out "$tmp/tls.crt" 2>/dev/null
    kc -n hoop create secret tls hoop-sidecar-tls --cert="$tmp/tls.crt" --key="$tmp/tls.key" >/dev/null
    rm -rf "$tmp"
fi
c_ok "namespaces hoop and demo"

c_step "Installing the sidecar chart with values.yaml"
helm --kube-context "$CTX" upgrade --install hoop-kube ../../helm-chart/chart/sidecar \
    -n hoop -f values.yaml --wait --timeout 3m >/dev/null
kc -n demo rollout status deploy/web --timeout=2m >/dev/null
c_ok "sidecar ready on https://127.0.0.1:8443"

SIDECAR_HOST="" APISERVER_HOST=""
if [[ -n "$TUNNEL" ]]; then
    c_step "Starting the quick tunnels"
    # One to the lane, over HTTP/2 to its TLS listener, and one straight to
    # the API server, which the sandbox must not reach. Both enter through
    # the k3d load balancer, which passes TLS through untouched.
    lb=k3d-$CLUSTER-serverlb
    docker rm -f "${TUNNELS[@]}" >/dev/null 2>&1 || true
    docker run -d --name "${TUNNELS[0]}" --network "k3d-$CLUSTER" "$CLOUDFLARED" \
        tunnel --no-autoupdate --url "https://$lb:443" --http2-origin --no-tls-verify >/dev/null
    docker run -d --name "${TUNNELS[1]}" --network "k3d-$CLUSTER" "$CLOUDFLARED" \
        tunnel --no-autoupdate --url "https://$lb:6443" --no-tls-verify >/dev/null
    tunnel_host() {
        local host=""
        for _ in $(seq 60); do
            host=$(docker logs "$1" 2>&1 | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' \
                | tail -1 | sed 's|https://||') || true
            [[ -n "$host" ]] && { echo "$host"; return; }
            sleep 1
        done
        die "no trycloudflare.com URL in the $1 log after 60 s"
    }
    SIDECAR_HOST=$(tunnel_host "${TUNNELS[0]}")
    APISERVER_HOST=$(tunnel_host "${TUNNELS[1]}")
    c_ok "sidecar https://$SIDECAR_HOST, API server https://$APISERVER_HOST"
fi

(umask 077; cat > .env) <<EOF
# Written by ./run.sh.
CONTEXT=$CTX
SIDECAR_HOST=$SIDECAR_HOST
APISERVER_HOST=$APISERVER_HOST
EOF

cat <<EOF

ready

  Sidecar        https://127.0.0.1:8443${SIDECAR_HOST:+ and https://$SIDECAR_HOST}
  Next           $([[ -n "$TUNNEL" ]] && echo "./e2e.sh (needs ANTHROPIC_API_KEY)" || echo "./demo.sh")

  Audit trail, one JSON line per row:
      kubectl --context $CTX -n hoop logs deploy/hoop-kube-hoopsidecar | grep '^{'

  Teardown:      ./run.sh down
EOF
