#!/bin/bash
cat - > xtdb.edn <<EOF
{:xtdb.http-server/server {}
 :xtdb.calcite/server {}}
EOF
echo "--> STARTING DATABASE..."
2>/dev/null 1>&2 java -jar /app/xtdb-in-memory-1.22.0-$(uname -m)/xtdb-in-memory.jar &
until curl -s -f -o /dev/null "http://127.0.0.1:3000/_xtdb/status"
do
  echo -n "."
  sleep 1
done
echo " done!"
echo "--> STARTING GATEWAY ..."

export PORT=8009
export PROFILE=dev
export GIN_MODE=release
export XTDB_ADDRESS=http://127.0.0.1:3000 
/app/hoop start gateway &

unset PORT PROFILE GIN_MODE XTDB_ADDRESS

until curl -s -f -o /dev/null "http://127.0.0.1:8009/agents"
do
    sleep 0.2
done
echo "--> GATEWAY IS READY!"
echo "--> STARTING AGENT ..."
/app/hoop start agent
