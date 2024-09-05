#!/bin/bash
set -eo pipefail

: "${HOOP_PUBLIC_HOSTNAME:? Required env HOOP_PUBLIC_HOSTNAME}"

mkdir -p /hoopdata/tls
if [ -z "${HOOP_PUBLIC_HOSTNAME}" ]; then
    echo "--> the env HOOP_PUBLIC_URL is required on .env file!"
    exit 1
fi

if [ ! -f /hoopdata/zitadel-master.key ]; then
    openssl rand -base64 22 | tr -d '\n' > /hoopdata/zitadel-master.key
    chmod 0444 /hoopdata/zitadel-master.key || true
    chown root: /hoopdata/zitadel-master.key
fi

if [ -n "${NGINX_TLS_CA}" ]; then
    : "${NGINX_TLS_KEY:? Required env NGINX_TLS_KEY}"
    : "${NGINX_TLS_CERT:? Required env NGINX_TLS_CERT}"
    echo -n ${NGINX_TLS_CA} |base64 -d > /hoopdata/tls/ca.crt
    echo -n ${NGINX_TLS_KEY} | base64 -d > /hoopdata/tls/server.key
    echo -n ${NGINX_TLS_CERT} | base64 -d  > /hoopdata/tls/server.crt
    echo "--> skip tls provisioning, loaded certs from environment variables!"
    exit 0
fi

if [ -f /hoopdata/tls/ca.crt ]; then
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
DNS.2 = idp
IP.1 = 127.0.0.1
IP.2 = ${HOOP_PUBLIC_HOSTNAME}
EOF
openssl x509 -req \
    -in /hoopdata/tls/server.csr \
    -CA /hoopdata/tls/ca.crt \
    -CAkey /hoopdata/tls/ca.key -CAcreateserial \
    -out /hoopdata/tls/server.crt -days 730 -sha256 \
    -extfile /hoopdata/tls/server.v3.ext

echo "--> certificates (ca.crt, server.crt, server.key) provisioned with success!"
