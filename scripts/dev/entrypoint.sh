#!/bin/bash

cd /app/

echo "--> STARTING GATEWAY ..."
/app/hooplinux start gateway &

until curl -s -f -o /dev/null "http://127.0.0.1:8009/api/healthz"
do
  sleep 1
done
echo "--> GATEWAY IS UP"

# org multi connection token
# HOOP_KEY=http://default:xagt-zXQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NE@127.0.0.1:8010 hoop run
psql $POSTGRES_DB_URI <<EOT
INSERT INTO agents (org_id, id, name, mode, key, key_hash, status)
    VALUES ((SELECT id from private.orgs), '3BD2DAC4-42FA-4C8A-A842-2DCD5566D54B', '_default', 'multi-connection', 'xagt-zXQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NE', '111f61419517bb0bef18c6e4f3a8767c7589a3a3a7d0313020ccfb67cf74edf2', 'DISCONNECTED')
    ON CONFLICT DO NOTHING;
EOT

# don't start a default agent if it's an org multi tenant setup
if [ "$ORG_MULTI_TENANT" == "true" ]; then
  sleep infinity
  exit $?
fi

psql $POSTGRES_DB_URI <<EOT
INSERT INTO agents (org_id, id, name, mode, key_hash, status)
    VALUES ((SELECT id from private.orgs), '75122BCE-F957-49EB-A812-2AB60977CD9F', 'default', 'standard', '7854115b1ae448fec54d8bf50d3ce223e30c1c933edcd12767692574f326df57', 'DISCONNECTED')
    ON CONFLICT DO NOTHING;
EOT

echo "--> STARTING AGENT ..."
# get digest of the agent secret key
# echo -n xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg |sha256sum
HOOP_KEY="http://default:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@127.0.0.1:8010?mode=standard" /app/hooplinux start agent &

sleep infinity
