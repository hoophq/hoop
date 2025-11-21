#!/bin/bash

set -eo pipefail

cd /app/

echo "--> STARTING GATEWAY ..."
/app/bin/hooplinux start gateway &

until curl -s -f -k -o /dev/null "$API_URL/api/healthz"
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
EOT

# export RUST_LOG=debug
# inside the docker container the hoop_rs binary is located in /app/bin/hoop_rs
export PATH="/app/bin:$PATH"

echo "--> STARTING AGENT ..."
# get digest of the agent secret key
# echo -n xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg |sha256sum
# export HOOP_TLS_HOOP_TLS_SKIP_VERIFY=true
# mkdir $HOME/.hoop
# cat > $HOME/.hoop/config.toml <<EOF
# Hostname = "dev.hoop.dev"
# Token = "grpcs://default:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@dev.hoop.dev:8010?mode=standard"
# TlsCertificateSource = "External"
# TlsCertificateFile = "/app/ui/public/server-full.crt"
# TlsPrivateKeyFile = "/app/ui/public/server.key"
# TlsVerifyStrict = true
# EOF
# export HOOP_TLSCA=file:///app/ui/public/ca.crt
# export HOOP_TLS_SKIP_VERIFY=true
HOOP_KEY="grpcs://default:xagt-zKQQA9PAjCVJ4O8VlE2QZScNEbfmFisg_OerkI21NEg@127.0.0.1:8010?mode=standard" /app/bin/hooplinux start agent &

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
