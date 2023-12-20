#!/bin/bash

cd /app/

echo "--> STARTING GATEWAY ..."
/app/hooplinux start gateway --listen-admin-addr "0.0.0.0:8099" &

until curl -s -f -o /dev/null "http://127.0.0.1:8009/api/healthz"
do
  sleep 1
done
echo "done"

# don't start a default agent if it's an org multi tenant setup
if [ "$ORG_MULTI_TENANT" == "true" ]; then
  sleep infinity
  exit $?
fi

psql $POSTGRES_DB_URI <<EOT
INSERT INTO agents (org_id, id, name, mode, token, status)
    VALUES ((SELECT id from private.orgs), '75122BCE-F957-49EB-A812-2AB60977CD9F', 'default', 'standard', '7854115b1ae448fec54d8bf50d3ce223e30c1c933edcd12767692574f326df57', 'DISCONNECTED')
    ON CONFLICT DO NOTHING;
EOT

echo "--> STARTING AGENT ..."
# get digest of the agent secret key
# echo -n xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg |sha256sum
HOOP_DSN="http://default:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@127.0.0.1:8010?mode=standard" /app/hooplinux start agent &

sleep infinity
