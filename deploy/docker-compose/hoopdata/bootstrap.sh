#!/bin/bash

set -e

if [ ! -f /hoopdata/zitadel-master.key ]; then
    openssl rand -base64 22 | tr -d '\n' > /hoopdata/zitadel-master.key
    chmod 0400 /hoopdata/zitadel-master.key || true
fi

if [ -f /hoopdata/tls/ca.crt ]; then
    if [ ! -f /hoopdata/tls/server.crt ] || [ ! -f /hoopdata/tls/server.key ]; then
        echo "--> ca.crt is present but server.crt or server.key are missing"
        echo "move these files "
        exit 1
    fi
    echo "--> skip tls provisioning, certificate (ca.crt) already exists!"
    exit 0
fi

# Root CA
openssl genrsa -out /hoopdata/tls/ca.key 4096
openssl req -x509 -new -nodes -key /hoopdata/tls/ca.key -sha256 -days 1826 -out /hoopdata/tls/ca.crt -subj '/CN=Hoopdev Root CA/C=US/ST=Delaware/O=Decimals Inc'

# Server
openssl req -new -nodes -out /hoopdata/tls/server.csr -newkey rsa:4096 -keyout /hoopdata/tls/server.key -subj '/CN=Hoop Gateway/C=US/ST=Delaware/O=Decimals Inc'
cat > /hoopdata/tls/server.v3.ext << EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names
[alt_names]
DNS.1 = gateway
DNS.2 = ${HOOP_PUBLIC_HOSTNAME}
DNS.3 = auth.${HOOP_PUBLIC_HOSTNAME}
IP.1 = 127.0.0.1
EOF
openssl x509 -req \
    -in /hoopdata/tls/server.csr \
    -CA /hoopdata/tls/ca.crt \
    -CAkey /hoopdata/tls/ca.key -CAcreateserial \
    -out /hoopdata/tls/server.crt -days 730 -sha256 \
    -extfile /hoopdata/tls/server.v3.ext

echo "--> certificates (ca.crt, server.crt, server.key) provisioned with success!"
