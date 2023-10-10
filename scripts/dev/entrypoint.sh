#!/bin/bash

cd /app/
java $JVM_OPTS -Dlogback.configurationFile=/app/logback.xml \
     -jar /app/xtdb-pg.jar &
echo "--> STARTING GATEWAY ..."

/app/hooplinux start gateway --listen-admin-addr "0.0.0.0:8099" &

until curl -s -f -o /dev/null "http://127.0.0.1:8009/api/healthz"
do
  sleep 1
done
echo "done"
echo "--> STARTING AGENT (xtdb) ..."

curl -s -f -o /dev/null "http://127.0.0.1:3001/_xtdb/status" || { echo "THE XTDB IS DOWN"; exit 1; }
curl -s -f -o /dev/null "${NODE_API_URL}" || { echo "THE NODE API IS DOWN AT ${NODE_API_URL}"; exit 1; }

AUTO_REGISTER=1 /app/hooplinux start agent &

ORG_ID=$(curl -s -XPOST '127.0.0.1:3001/_xtdb/query' \
  -H 'Content-Type: application/edn' \
  -H 'Accept: application/json' \
  --data-raw '{:query {
    :find [(pull ?org [*])]
    :where [[?org :org/name]]
  }}' | jq '.[][]["xt/id"]' -r)

PGPASSWORD=$PG_PASSWORD psql -h $PG_HOST -U $PG_USER --port $PG_PORT $PG_DB <<EOT
DELETE FROM agents WHERE id = '75122BCE-F957-49EB-A812-2AB60977CD9F';
INSERT INTO agents ("orgId", "createdBy", name, mode, token, status, id, "createdAt", "updatedAt")
VALUES ('${ORG_ID}', 'bot-dev', 'dev', 'standard', '7854115b1ae448fec54d8bf50d3ce223e30c1c933edcd12767692574f326df57', 'DISCONNECTED', '75122BCE-F957-49EB-A812-2AB60977CD9F', NOW(), NOW());
EOT

echo "--> STARTING AGENT (postgres) ..."
unset AUTO_REGISTER
# get digest of the agent secret key
# echo -n xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NE |sha256sum
HOOP_DSN="http://dev:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@127.0.0.1:8010?mode=standard&v2=true" /app/hooplinux start agent &

sleep infinity
