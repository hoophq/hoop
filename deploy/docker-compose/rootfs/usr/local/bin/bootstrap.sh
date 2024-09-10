#!/bin/bash

: "${HOOP_PUBLIC_HOSTNAME:? Required env HOOP_PUBLIC_HOSTNAME}"

function print_and_exit() {
  echo "error: $1"
  exit 1
}

# pre-flight checks
if [[ $HOOP_PUBLIC_HOSTNAME == "127.0.0.1" ]] && [[ $HOOP_TLS_MODE == "enabled" ]]; then
    print_and_exit "use your local machine host for tls setup"
fi

HOSTNAME_IP_ADDR=0
if [[ $HOOP_PUBLIC_HOSTNAME =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    HOSTNAME_IP_ADDR=1
else
    nslookup $HOOP_PUBLIC_HOSTNAME > /dev/null || \
        print_and_exit "not able to resolve address $HOOP_PUBLIC_HOSTNAME. Check if the HOOP_PUBLIC_HOSTNAME is a valid DNS name."
fi

nc -z $HOOP_PUBLIC_HOSTNAME 80 && print_and_exit "Port 80 is being used by another process."
nc -z $HOOP_PUBLIC_HOSTNAME 443 && print_and_exit "Port 443 is being used by another process."

set -eo pipefail

mkdir -p /hoopdata/tls
if [ -z "${HOOP_PUBLIC_HOSTNAME}" ]; then
    echo "--> the env HOOP_PUBLIC_HOSTNAME is required on .env file!"
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
DNS.3 = nginx
DNS.4 = ${HOOP_PUBLIC_HOSTNAME}
IP.1 = 127.0.0.1
EOF

if [[ $HOSTNAME_IP_ADDR == "1" ]]; then
    echo "IP.2 = $HOOP_PUBLIC_HOSTNAME" >> /hoopdata/tls/server.v3.ext
fi

openssl x509 -req \
    -in /hoopdata/tls/server.csr \
    -CA /hoopdata/tls/ca.crt \
    -CAkey /hoopdata/tls/ca.key -CAcreateserial \
    -out /hoopdata/tls/server.crt -days 730 -sha256 \
    -extfile /hoopdata/tls/server.v3.ext

echo "--> certificates (ca.crt, server.crt, server.key) provisioned with success!"
