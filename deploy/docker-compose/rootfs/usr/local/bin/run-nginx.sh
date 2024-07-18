#!/bin/bash

: "${HOOP_PUBLIC_HOSTNAME:? Required env HOOP_PUBLIC_HOSTNAME}"

set -e

mkdir -p /etc/certs
cp /hoopdata/tls/server.crt /etc/certs/server.crt
cp /hoopdata/tls/server.key /etc/certs/server.key
sed "s|HOOP_PUBLIC_HOSTNAME_PLACEHOLDER|${HOOP_PUBLIC_HOSTNAME}|g" -i /etc/nginx/nginx.conf
nginx -g "daemon off;"
