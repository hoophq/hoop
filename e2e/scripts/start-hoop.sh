#!/usr/bin/env bash
#
# Starts the control plane for the e2e suite (`hoop start control-plane` on
# :8019), on a fresh embedded PGlite database. Playwright's webServer runs it.
#
# HOOP_BIN       the hoop binary to test (required)
# STATIC_UI_PATH the built web UI (default: ../dist/dev/resources/public,
#                what `make build-dev-webapp` produces)
set -euo pipefail

E2E_DIR="$(cd "$(dirname "$0")/.." && pwd)"

if [[ -z "${HOOP_BIN:-}" || ! -x "$HOOP_BIN" ]]; then
  echo "FAIL: set HOOP_BIN to an executable hoop binary (got '${HOOP_BIN:-}')" >&2
  exit 1
fi

export STATIC_UI_PATH="${STATIC_UI_PATH:-$E2E_DIR/../dist/dev/resources/public}"
if [[ ! -f "$STATIC_UI_PATH/index.html" ]]; then
  echo "FAIL: no web UI at $STATIC_UI_PATH (run make build-dev-webapp)" >&2
  exit 1
fi

# Every run starts from an empty database, so the first-admin setup flow runs.
DATA_DIR="$E2E_DIR/.data/control-plane"
rm -rf "$DATA_DIR"
mkdir -p "$DATA_DIR/pgdata" "$DATA_DIR/sessions"

export DO_NOT_TRACK=1
export GIN_MODE=release
export AUTH_METHOD=local
export PORT=8019
export API_URL="http://127.0.0.1:8019"
export POSTGRES_DB_URI="pglite://$DATA_DIR/pgdata"
export PLUGIN_AUDIT_PATH="$DATA_DIR/sessions"

exec "$HOOP_BIN" start control-plane
