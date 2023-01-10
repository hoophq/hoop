#!/bin/bash

trap ctrl_c INT

function ctrl_c() {
    ps aux |grep -i /tmp/hoop |awk {'print $2'} |xargs kill
    exit 130
}

echo "--> STARTING XTDB..."
docker run --name xtdb --rm -d -p 3001:3000 runops/xtdb-in-memory:$(uname -m) 2&> /dev/null
until curl -s -f -o /dev/null "http://127.0.0.1:3001/_xtdb/status"
do
  echo -n "."
  sleep 1
done
echo " done!"
echo "--> STARTING GATEWAY ..."

# export GOOGLE_APPLICATION_CREDENTIALS_JSON=$(cat ../misc/profiles/hoop/dlp-serviceaccount.json)
export PORT=8009
export PROFILE=dev
export XTDB_ADDRESS=http://127.0.0.1:3001
export PLUGIN_AUDIT_PATH=/tmp/hoopsessions
export GIN_MODE=debug
# require to run npm install && npm run release:hoop-ui
export STATIC_UI_PATH=../webapp/resources/public/
go build -o /tmp/hoop client/main.go
/tmp/hoop start gateway &

unset PORT XTDB_ADDRESS PLUGIN_AUDIT_PATH

until curl -s -f -o /dev/null "http://127.0.0.1:8009/api/agents"
do
    sleep 1
done
echo "--> GATEWAY IS READY!"
echo "--> STARTING AGENT ..."
/tmp/hoop start agent
