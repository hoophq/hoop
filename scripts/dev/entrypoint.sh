#!/bin/bash

set -eo pipefail

cd /app/

echo "--> STARTING GATEWAY ..."
/app/bin/hooplinux start gateway &

until curl -s -f -o /dev/null "http://127.0.0.1:8009/api/healthz"
do
  sleep 1
done
echo "--> GATEWAY IS UP"

# don't start a default agent if it's an org multi tenant setup
if [ "$ORG_MULTI_TENANT" == "true" ]; then
  sleep infinity
  exit $?
fi

# add ec2 metadata mock server
# https://github.com/aws/amazon-ec2-metadata-mock
if [ -n "$AWS_SESSION_TOKEN_JSON" ]; then
  echo "--> ADDING EC2 METADATA MOCK SERVER ..."
  ip addr add 169.254.169.254/32 dev eth0

  echo -e "$AWS_SESSION_TOKEN_JSON" | sed 's|SessionToken|Token|g' | jq '{
  "metadata": {
    "values": {
      "iam-security-credentials": .Credentials
    }
  }
}' > /tmp/metadata-mock-config.json
  ec2-metadata-mock --hostname 169.254.169.254 --port 80 -c /tmp/metadata-mock-config.json &
fi

psql $POSTGRES_DB_URI <<EOT
INSERT INTO private.agents (org_id, id, name, mode, key_hash, status)
    VALUES ((SELECT id from private.orgs), '75122BCE-F957-49EB-A812-2AB60977CD9F', 'default', 'standard', '7854115b1ae448fec54d8bf50d3ce223e30c1c933edcd12767692574f326df57', 'DISCONNECTED')
    ON CONFLICT DO NOTHING;

INSERT INTO private.agents (org_id, id, name, mode, key_hash, status)
    VALUES ((SELECT id from private.orgs), 'a3a4c6d6-db7d-5e84-881e-a360eca782a4', 'rdpagent', 'standard', 'ee0c29868c274034d4182b32edfa603bdc4ba4a2524f7e1860a3f7e0e6854bb9', 'DISCONNECTED')
    ON CONFLICT DO NOTHING;
EOT

echo "--> STARTING AGENT ..."
# get digest of the agent secret key
# echo -n xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg |sha256sum
HOOP_KEY="grpc://default:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@127.0.0.1:8010?mode=standard" /app/bin/hooplinux start agent &

# --- Your Rust process here ---
export RUST_LOG=debug
export GATEWAY_URL="ws://127.0.0.1:8009/api/ws"  # Explicitly set the gateway URL

HOOP_KEY="grpc://rdpagent:xagt-C2Fr6Ah38dN2x9sKwTn1bgiw9BfwY6xd_gWJYtzGea0@127.0.0.1:8010?mode=standard" \
/app/bin/hoop_rs &
pids+=($!)

# Wait and check if the process started successfully
sleep 2
if ! kill -0 $! 2>/dev/null; then
    echo "ERROR: hoop_rs failed to start"
    exit 1
fi
echo "hoop_rs started with PID: $!"

echo "--> STARTING SSHD SERVER ..."

# add default password for root user
echo 'root:1a2b3c4d' | chpasswd

sed -i 's|#PubkeyAuthentication yes|PubkeyAuthentication yes|g' /etc/ssh/sshd_config

sed -i 's|#PermitRootLogin prohibit-password|PermitRootLogin yes|g' /etc/ssh/sshd_config
sed -i 's|#AllowTcpForwarding yes|AllowTcpForwarding yes|g' /etc/ssh/sshd_config
sed -i 's|#AllowAgentForwarding yes|AllowAgentForwarding yes|g' /etc/ssh/sshd_config
sed -i 's|#PermitUserEnvironment no|PermitUserEnvironment yes|g' /etc/ssh/sshd_config
sed -i 's|#PermitTunnel no|PermitTunnel yes|g' /etc/ssh/sshd_config

# generate an ssh key if it does not exists
if [ ! -f /root/.ssh/id_rsa ]; then
  ssh-keygen -t rsa -b 4096 -f /root/.ssh/id_rsa -q -N ''
  cp /root/.ssh/id_rsa.pub /root/.ssh/authorized_keys
fi

/usr/sbin/sshd -D -o ListenAddress=0.0.0.0 &

sleep infinity
