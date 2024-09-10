#!/bin/bash

: "${HOOP_PUBLIC_HOSTNAME:? Required env HOOP_PUBLIC_HOSTNAME}"
: "${POSTGRES_DB_URI:? Required env POSTGRES_DB_URI}"

set -e

mkdir -p /etc/ssl/certs
cp /hoopdata/tls/ca.crt /etc/ssl/certs/ca-certificates.crt

export SECRET_KEY=xagt-$(LC_ALL=C tr -dc A-Za-z0-9_ < /dev/urandom | head -c 43 | xargs)
set -eo pipefail
export SECRET_KEY_HASH=$(printenv SECRET_KEY | tr -d '\n' | sha256sum | awk {'print $1'})
psql -v ON_ERROR_STOP=1 "$POSTGRES_DB_URI" <<EOF
BEGIN;
DELETE FROM agents WHERE name = 'system';
INSERT INTO agents (id, org_id, name, mode, key_hash, status)
    VALUES ('CB2D463F-B2D2-4FE4-B612-76444C85166C', (SELECT id from private.orgs), 'system', 'standard', '$(printenv SECRET_KEY_HASH | tr -d "\n")', 'DISCONNECTED')
    ON CONFLICT DO NOTHING;
COMMIT;
EOF

export GRPC_URL=${HOOP_PUBLIC_HOSTNAME}:80
SCHEME=grpc
if [[ "$HOOP_TLS_MODE" == "enabled" ]]; then
    export GRPC_URL=${HOOP_PUBLIC_HOSTNAME}:443
    SCHEME=grpcs
elif [[ "$HOOP_PUBLIC_HOSTNAME" == "127.0.0.1" ]]; then
    export GRPC_URL=nginx:80
fi

export HOOP_KEY=${SCHEME}://system:$(printenv SECRET_KEY | tr -d '\n')@${GRPC_URL}?mode=standard
hoop start agent
