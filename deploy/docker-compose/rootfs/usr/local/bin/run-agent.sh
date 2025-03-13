#!/bin/bash

set -e

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

if [ -z $SCHEME ]; then
    SCHEME=grpcs
    if [ -z $TLS_KEY ]; then
        SCHEME=grpc
    fi
fi

if [ -z $GRPC_URL ]; then
    export GRPC_URL=gateway:8010
fi

export HOOP_KEY=${SCHEME}://system:$(printenv SECRET_KEY | tr -d '\n')@${GRPC_URL}?mode=standard
hoop start agent
