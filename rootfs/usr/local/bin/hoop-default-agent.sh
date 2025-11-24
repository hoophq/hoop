#!/bin/bash

: "${POSTGRES_DB_URI:?env is required}"
: "${DEFAULT_AGENT_GRPC_SCHEME:-"grpc"}"
: "${DEFAULT_AGENT_GRPC_HOST:-"127.0.0.1:8010"}"
: "${DEFAULT_AGENT_GRPC_SKIP_VERIFY:-"false"}"

SECRET_KEY=xagt-$(LC_ALL=C tr -dc A-Za-z0-9_ < /dev/urandom | head -c 43 | xargs)
set -eo pipefail
SECRET_KEY_HASH=$(echo -n $SECRET_KEY | sha256sum |awk {'print $1'})

echo "--> starting default agent ..."

psql -v ON_ERROR_STOP=1 "$POSTGRES_DB_URI" <<EOF
BEGIN;
DELETE FROM private.agents WHERE name = 'default';
INSERT INTO private.agents (id, org_id, name, mode, key_hash, status)
    VALUES ('e72e6fba-8ed3-5cde-9ff6-36f062e1e51b', (SELECT id from private.orgs), 'default', 'standard', '$SECRET_KEY_HASH', 'DISCONNECTED')
    ON CONFLICT DO NOTHING;
COMMIT;
EOF

WS_SCHEME=ws
if [[ "$DEFAULT_AGENT_GRPC_SCHEME" == "grpcs" ]]; then
  WS_SCHEME=wss
fi

export HOOP_GATEWAY_URL="${WS_SCHEME}://127.0.0.1:8009"
if [[ -n $DEFAULT_AGENT_GATEWAY_URL ]]; then
  export HOOP_GATEWAY_URL=$DEFAULT_AGENT_GATEWAY_URL
fi

export HOOP_TLS_SKIP_VERIFY=${DEFAULT_AGENT_GRPC_SKIP_VERIFY}
export HOOP_TLSCA=$DEFAULT_AGENT_GRPC_TLS_CA
export HOOP_KEY="${DEFAULT_AGENT_GRPC_SCHEME}://default:${SECRET_KEY}@${DEFAULT_AGENT_GRPC_HOST}?mode=standard"
hoop start agent
