#!/usr/bin/env bash
# Brings up the local RDP PII-guard demo stack end to end:
#
#   1. start db + gateway + presidio (agent waits)
#   2. wait for the gateway to bootstrap its org + run migrations
#   3. register a "default" agent (key_hash) directly on the gateway DB and
#      mint the matching agent secret (this is the production registration
#      path; the agent never touches the DB itself)
#   4. enable the experimental.rdp_pii_guard flag for the org and restart the
#      gateway so it re-warms the flag cache
#   5. start the agent with the matching HOOP_KEY
#
# The agent image bundles its OCR engine and runs the agentrs PII gate; it
# calls Presidio at MSPRESIDIO_ANALYZER_URL. The gateway pushes the guard
# enable + policy (RDP_PII_GUARD_POLICY) via SessionStarted metadata.
#
# Usage: scripts/dev/run-pii-demo.sh
set -euo pipefail

COMPOSE_FILE="deploy/docker-compose/docker-compose.pii-demo.yml"
ENV_FILE="deploy/docker-compose/.pii-demo.env"
FLAG="experimental.rdp_pii_guard"
AGENT_ID="e72e6fba-8ed3-5cde-9ff6-36f062e1e51b"
dc() { docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_FILE}" "$@"; }

# Stable secret for the run; the agent uses it via HOOP_KEY, the gateway stores
# its sha256 as key_hash. Regenerated each invocation. `head` closing the pipe
# early would SIGPIPE `tr` under pipefail, so generate without a head/tr pipe.
AGENT_SECRET="xagt-$(LC_ALL=C openssl rand -hex 21)"
AGENT_SECRET_HASH="$(printf '%s' "${AGENT_SECRET}" | shasum -a 256 | awk '{print $1}')"
# Persist for compose interpolation (and so follow-up `docker compose` commands
# with --env-file work without re-running this script).
printf 'AGENT_SECRET=%s\n' "${AGENT_SECRET}" > "${ENV_FILE}"

echo "[demo] starting db + gateway + presidio (agent held back)"
dc up -d db gateway presidio-analyzer

echo "[demo] waiting for gateway health"
until [ "$(dc ps gateway --format '{{.Health}}' 2>/dev/null)" = "healthy" ]; do
  sleep 2
done

echo "[demo] registering 'default' agent + enabling ${FLAG} (via db container)"
# Run SQL on the postgres container itself -- the gateway runtime image does
# not ship psql. The agent never touches the DB; this is gateway-side setup.
dc exec -T db psql --quiet -v ON_ERROR_STOP=1 -U postgres -d hoopdb >/dev/null <<SQL
BEGIN;
DELETE FROM private.agents WHERE name = 'default';
INSERT INTO private.agents (id, org_id, name, mode, key_hash, status)
  VALUES ('${AGENT_ID}', (SELECT id FROM private.orgs), 'default', 'standard', '${AGENT_SECRET_HASH}', 'DISCONNECTED');
INSERT INTO private.org_feature_flags (org_id, name, enabled, updated_at, updated_by)
  SELECT id, '${FLAG}', true, now(), 'pii-demo' FROM private.orgs
  ON CONFLICT (org_id, name)
  DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by;
COMMIT;
SQL

echo "[demo] restarting gateway to re-warm the flag cache"
dc restart gateway
until [ "$(dc ps gateway --format '{{.Health}}' 2>/dev/null)" = "healthy" ]; do
  sleep 2
done

echo "[demo] starting agent (HOOP_KEY wired)"
dc up -d agent

cat <<EOF

[demo] stack is up.
  Gateway UI/API : http://localhost:8009
  RDP target     : configure a connection to 192.168.55.120:3389 (user hooptest)
  Guard policy   : ${RDP_PII_GUARD_POLICY:-redact} (set on the gateway service)
  Flag           : ${FLAG} = enabled

  Watch the gate:  docker compose -f ${COMPOSE_FILE} logs -f agent
EOF
