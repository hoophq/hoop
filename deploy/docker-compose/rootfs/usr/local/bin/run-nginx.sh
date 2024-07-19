#!/bin/bash

: "${HOOP_PUBLIC_HOSTNAME:? Required env HOOP_PUBLIC_HOSTNAME}"

set -e

reload-nginx() {
    until curl -k -s -f -o /dev/null "http://gateway:8009/api/healthz"; do
      sleep 1
    done
    sed "s|127.0.0.1:8009|gateway:8009|g;s|127.0.0.1:8010|gateway:8010|g" -i /etc/nginx/nginx.conf
    echo "gateway is alive, reloading nginx ..."
    nginx -s reload
}

reload-nginx &

mkdir -p /etc/certs
cp /hoopdata/tls/server.crt /etc/certs/server.crt
cp /hoopdata/tls/server.key /etc/certs/server.key
sed "s|HOOP_PUBLIC_HOSTNAME_PLACEHOLDER|${HOOP_PUBLIC_HOSTNAME}|g" -i /etc/nginx/nginx.conf
nginx -g "daemon off;"
