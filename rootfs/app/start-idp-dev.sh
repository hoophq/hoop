#!/bin/bash
cat - > xtdb.edn <<EOF
{:xtdb.http-server/server {:port 3001}}
EOF
echo "--> STARTING DATABASE..."
curl -s -L https://releases.hoop.dev/xtdb-in-memory-1.22.0-$(uname -m).tar.gz -o /app/xtdb-in-memory-1.22.0-$(uname -m).tar.gz
tar -xf /app/xtdb-in-memory-1.22.0-$(uname -m).tar.gz -C /app/
apt-get update -y && apt-get install openjdk-11-jre -y
2>/dev/null 1>&2 java -jar /app/xtdb-in-memory-1.22.0-$(uname -m)/xtdb-in-memory.jar &
until curl -s -f -o /dev/null "http://127.0.0.1:3001/_xtdb/status"
do
  echo -n "."
  sleep 1
done
echo " done!"
echo "--> STARTING GATEWAY ..."

export PORT=8009
export GIN_MODE=release
export XTDB_ADDRESS=http://127.0.0.1:3001
/app/hoop start gateway