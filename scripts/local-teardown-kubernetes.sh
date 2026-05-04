#!/usr/bin/env bash
# local-spiffe-down.sh — clean teardown.
set -euo pipefail
pkill -f "port-forward.*hoopgateway" 2>/dev/null || true
helm -n hoop          uninstall hoopagent  2>/dev/null || true
helm -n hoop          uninstall hoop       2>/dev/null || true
helm -n spire-mgmt    uninstall spire      2>/dev/null || true
helm -n spire-mgmt    uninstall spire-crds 2>/dev/null || true
kubectl delete ns hoop spire-mgmt spire-server spire-system --ignore-not-found
