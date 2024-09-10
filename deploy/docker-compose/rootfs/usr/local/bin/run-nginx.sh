#!/bin/bash

: "${HOOP_PUBLIC_HOSTNAME:? Required env HOOP_PUBLIC_HOSTNAME}"

set -eo pipefail

rm -f /etc/nginx/nginx.conf
ln -s /etc/nginx/nginx-default.conf /etc/nginx/nginx.conf
if [[ "$HOOP_TLS_MODE" == "enabled" ]]; then
    rm -f /etc/nginx/nginx.conf
    ln -s /etc/nginx/nginx-tls.conf /etc/nginx/nginx.conf
fi

reload-nginx() {
    until curl -k -s -f -o /dev/null "http://gateway:80/healthz"; do
      sleep 1
    done
    sed "s|server 127.0.0.1:|server gateway:|g" -i /etc/nginx/nginx.conf
    echo "gateway is alive, reloading nginx ..."
    nginx -s reload
}

echo "--> starting nginx ..."
reload-nginx &

mkdir -p /etc/certs
cp /hoopdata/tls/server.crt /etc/certs/server.crt
cp /hoopdata/tls/server.key /etc/certs/server.key
sed "s|HOOP_PUBLIC_HOSTNAME_PLACEHOLDER|${HOOP_PUBLIC_HOSTNAME}|g" -i /etc/nginx/nginx.conf
nginx -g "daemon off;"
