#!/usr/bin/env bash
#
# Starts one hoop instance for the e2e suite, on a fresh embedded PGlite
# database. Playwright's webServer runs it once per mode:
#
#   gateway        `hoop start standalone`: gateway + agent on :8009
#   control-plane  `hoop start control-plane` on :8019
#
# HOOP_BIN       the hoop binary to test (required)
# STATIC_UI_PATH the built web UI (default: ../dist/dev/resources/public,
#                what `make build-dev-webapp` produces)
set -euo pipefail

MODE="${1:?usage: start-hoop.sh gateway|control-plane}"
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
DATA_DIR="$E2E_DIR/.data/$MODE"
rm -rf "$DATA_DIR"
mkdir -p "$DATA_DIR"

export DO_NOT_TRACK=1
export GIN_MODE=release
export AUTH_METHOD=local

case "$MODE" in
  gateway)
    # Standalone keeps its database and session files under $HOME/.hoop.
    export HOME="$DATA_DIR"
    exec "$HOOP_BIN" start standalone
    ;;
  control-plane)
    export PORT=8019
    export API_URL="http://127.0.0.1:8019"
    export POSTGRES_DB_URI="pglite://$DATA_DIR/pgdata"
    export PLUGIN_AUDIT_PATH="$DATA_DIR/sessions"
    mkdir -p "$DATA_DIR/pgdata" "$PLUGIN_AUDIT_PATH"
    exec "$HOOP_BIN" start control-plane
    ;;
  *)
    echo "FAIL: unknown mode '$MODE' (want gateway or control-plane)" >&2
    exit 1
    ;;
esac
