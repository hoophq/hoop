#!/bin/bash

trap ctrl_c INT

function ctrl_c() {
    ps aux |grep -i /tmp/hoop |awk {'print $2'} |xargs kill
    exit 130
}

echo "--> STARTING XTDB..."
docker run --name xtdb --rm -d -p 3001:3000 runops/xtdb-in-memory:$(uname -m) 2&> /dev/null
echo "--> STARTING GATEWAY ..."

#export GOOGLE_APPLICATION_CREDENTIALS_JSON=$(cat ../misc/profiles/dlp-serviceaccount.json)
#export TLS_CERT="$(cat /tmp/cert.pem |base64)"
#export TLS_CA="$(cat /tmp/ca.pem |base64)"
#export TLS_KEY="$(cat /Users/san/work/hoopdev/misc/privkey.pem |base64)"
export PORT=8009
export PROFILE=dev
export XTDB_ADDRESS=http://127.0.0.1:3001
export PLUGIN_AUDIT_PATH=/tmp/hoopsessions
export PLUGIN_INDEX_PATH=/tmp/hoopsessions/indexes
export PLUGIN_REGISTRY_URL=https://pluginregistry.s3.amazonaws.com/packages.json
export ORG_MULTI_TENANT=true
#export GIN_MODE=debug
# https://hoop.dev/docs/integrations/notifications-bridge
#export NOTIFICATIONS_BRIDGE_CONFIG='{"slackBotToken": "xoxb-<your-token>", "bridgeUrl": ""}'
#export GODEBUG='http2debug=2'
#export LOG_GRPC=1
#export LOG_ENCODING=json
#export LOG_LEVEL=debug
#export PYROSCOPE_AUTH_TOKEN=noop
#export PYROSCOPE_INGEST_URL=http://127.0.0.1:4040
export AGENT_SENTRY_DSN=
# require to run npm install && npm run release:hoop-ui
export STATIC_UI_PATH=../webapp/resources/public/
mkdir -p $HOME/.hoop/bin/
go build -o $HOME/.hoop/bin/hoop client/main.go
$HOME/.hoop/bin/hoop start gateway &

unset PORT XTDB_ADDRESS PLUGIN_AUDIT_PATH

until curl -s -f -o /dev/null "http://127.0.0.1:8009/api/agents"
do
    sleep 1
done
echo "--> GATEWAY IS READY!"
echo "--> STARTING AGENT ..."
export ENV_CONFIG='{"PG_HOST": "127.0.0.1", "PG_USER": "bob", "PG_PASS": "1a2b3c4d", "PG_PORT": "5444"}'
$HOME/.hoop/bin/hoop start agent
