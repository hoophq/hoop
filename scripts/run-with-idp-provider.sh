#!/bin/bash

trap ctrl_c INT

function ctrl_c() {
    ps aux |grep -i /tmp/hoop |awk {'print $2'} |xargs kill -9
    exit 130
}

docker run --name xtdb2 --rm -d -p 3001:3000 runops/xtdb-in-memory:$(uname -m) 2>/dev/null >/dev/null
until curl -s -f -o /dev/null "http://127.0.0.1:3001/_xtdb/status"
do
  echo -n "."
  sleep 1
done
echo " done!"
echo "--> STARTING GATEWAY ..."

export IDP_CLIENT_ID=hAgCLsLJeuEQLhNLpjrddd7NmSDHBIKF
export IDP_CLIENT_SECRET=A9xw9TOXTr_YvGoA_UA4jPoqYS2GQVF7RfUptvCYwiHIKkHx11ZATRX2mQ0_1tm8
export IDP_ISSUER=https://runops.us.auth0.com/
export IDP_AUDIENCE=https://runops.us.auth0.com/api/v2/
export PORT=8009
export XTDB_ADDRESS=http://127.0.0.1:3001
export PLUGIN_AUDIT_PATH=/tmp/hoopsessions
export API_URL=http://localhost:8009
export GIN_MODE=debug
go build -o /tmp/hoop github.com/runopsio/hoop/client
/tmp/hoop start gateway
