#!/bin/bash -ex

: "${PG_PORT:-5432}"
: "${PG_HOST:-127.0.0.1}"

trap ctrl_c INT

function ctrl_c() {
    docker stop hoopdev && docker rm hoopdev
    exit 130
}

GOOS=linux go build -o $HOME/.hoop/bin/hooplinux github.com/runopsio/hoop/client
docker stop hoopdev > /dev/null || true
docker run --name hoopdev \
  -p 3001:3001 \
  -p 8009:8009 \
  -p 8010:8010 \
  -e GIN_MODE=release \
  -e ORG_MULTI_TENANT=false \
  -e AUTO_REGISTER=1 \
  -e PORT=8009 \
  -e XTDB_ADDRESS=http://127.0.0.1:3001 \
  -e LOG_LEVEL=info \
  -e API_URL=http://localhost:8009 \
  -e IDP_CLIENT_ID=hAgCLsLJeuEQLhNLpjrddd7NmSDHBIKF \
  -e IDP_CLIENT_SECRET=A9xw9TOXTr_YvGoA_UA4jPoqYS2GQVF7RfUptvCYwiHIKkHx11ZATRX2mQ0_1tm8 \
  -e IDP_ISSUER=https://runops.us.auth0.com/ \
  -e IDP_AUDIENCE=https://runops.us.auth0.com/api/v2/ \
  -e PG_HOST=$PG_HOST \
  -e PG_USER=hoopapp \
  -e PG_PASSWORD=1a2b3c4d \
  -e PG_DB=hoopdev \
  -e PG_PORT=$PG_PORT \
  -e PLUGIN_REGISTRY_URL=https://pluginregistry.s3.amazonaws.com/packages.json \
  -v $HOME/.hoop/bin:/app/bin \
  --rm -it hoophq/hoop /app/bin/run-all.sh