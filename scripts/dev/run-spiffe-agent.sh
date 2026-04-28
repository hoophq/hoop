#!/bin/bash
# run-spiffe-agent.sh — start a host-side Hoop agent that authenticates
# to the dev gateway via a SPIFFE JWT-SVID (rather than the default DSN
# token). Assumes:
#
#   1. `make run-dev-spiffe-prep` has been run at least once to mint
#      the JWT and update .env.
#   2. `make run-dev` is running in another terminal and healthy.
#   3. `make build-dev-client` has produced $HOME/.hoop/bin/hoop.
#
# On each invocation this script idempotently seeds the dev DB with a
# `spiffe-agent` row and a matching `agent_spiffe_mappings` row, then
# runs the agent in the foreground with HOOP_KEY_FILE pointed at the
# minted JWT. Ctrl-C stops only the agent; the gateway keeps running.
set -eo pipefail

SPIFFE_DIR="${SPIFFE_DIR:-dist/dev/spiffe}"
TRUST_DOMAIN="${HOOP_SPIFFE_TRUST_DOMAIN:-local.test}"
SPIFFE_ID="${HOOP_SPIFFE_ID:-spiffe://local.test/agent/local-dev}"
AGENT_NAME="${HOOP_SPIFFE_AGENT_NAME:-spiffe-agent}"
AGENT_ID="${HOOP_SPIFFE_AGENT_ID:-11111111-2222-3333-4444-555555555555}"
APP_CONTAINER="${HOOPDEV_APP_CONTAINER:-hoopdev}"
DB_CONTAINER="${HOOPDEV_DB_CONTAINER:-hoopdevpg}"
HOOP_BIN="${HOOP_BIN:-$HOME/.hoop/bin/hoop}"

if [[ ! -f "$SPIFFE_DIR/agent.jwt" || ! -f "$SPIFFE_DIR/bundle.jwks" ]]; then
  echo "missing SPIFFE artifacts in $SPIFFE_DIR — run 'make run-dev-spiffe-prep' first"
  exit 1
fi

for c in "$APP_CONTAINER" "$DB_CONTAINER"; do
  if ! docker ps --format '{{.Names}}' | grep -q "^${c}$"; then
    echo "container '$c' not running — start 'make run-dev' in another terminal"
    exit 1
  fi
done

# Rebuild the client binary if it's missing or older than agent/config
# (which is where the HOOP_KEY_FILE loader lives). A stale binary silently
# falls through to "missing HOOP_KEY" because it doesn't know about the
# file-based loader yet.
needs_rebuild=0
if [[ ! -x "$HOOP_BIN" ]]; then
  needs_rebuild=1
else
  # find newest source file under agent/ and compare mtime to the binary
  newest=$(find agent common/clientconfig -type f -name '*.go' -newer "$HOOP_BIN" -print -quit 2>/dev/null || true)
  if [[ -n "$newest" ]]; then
    echo "==> $HOOP_BIN is stale (newer sources since last build, e.g. $newest)"
    needs_rebuild=1
  fi
fi
if [[ $needs_rebuild -eq 1 ]]; then
  echo "==> rebuilding hoop client"
  make build-dev-client
fi

echo "==> copying bundle.jwks into $APP_CONTAINER:/app/spiffe/"
docker exec "$APP_CONTAINER" mkdir -p /app/spiffe
docker cp "$SPIFFE_DIR/bundle.jwks" "$APP_CONTAINER":/app/spiffe/bundle.jwks

echo "==> seeding agent + spiffe mapping (idempotent) via $DB_CONTAINER"
PG_URI="$(docker exec "$APP_CONTAINER" printenv POSTGRES_DB_URI || true)"
if [[ -z "$PG_URI" ]]; then
  echo "POSTGRES_DB_URI not found in $APP_CONTAINER env; check .env and restart run-dev"
  exit 1
fi
docker exec -i "$DB_CONTAINER" psql "$PG_URI" <<EOT
INSERT INTO private.agents (org_id, id, name, mode, key_hash, status)
  VALUES (
    (SELECT id FROM private.orgs LIMIT 1),
    '$AGENT_ID',
    '$AGENT_NAME',
    'standard',
    'spiffe-managed-no-static-key',
    'DISCONNECTED'
  )
  ON CONFLICT (id) DO NOTHING;

INSERT INTO private.agent_spiffe_mappings
  (org_id, trust_domain, spiffe_id, agent_id, groups)
  VALUES (
    (SELECT id FROM private.orgs LIMIT 1),
    '$TRUST_DOMAIN',
    '$SPIFFE_ID',
    '$AGENT_ID',
    ARRAY['agents']
  )
  ON CONFLICT DO NOTHING;
EOT

# Fallback: if the bundle.jwks arrived after the gateway already
# initialised externaljwt (i.e. when prep was run after run-dev
# started), the refresh timer will pick it up within
# HOOP_SPIFFE_REFRESH_PERIOD (default 30s in prep.sh). No restart needed.

echo "==> starting host agent with HOOP_KEY_FILE=$SPIFFE_DIR/agent.jwt"
echo "    (Ctrl-C stops the agent; run-dev keeps going)"
echo

export HOOP_KEY_FILE="$PWD/$SPIFFE_DIR/agent.jwt"
export HOOP_GRPCURL="127.0.0.1:8010"
export HOOP_NAME="$AGENT_NAME"
export HOOP_TLS_SKIP_VERIFY="true"

exec "$HOOP_BIN" start agent
