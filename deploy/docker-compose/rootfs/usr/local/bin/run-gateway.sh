#!/bin/bash

# : "${HOOP_PUBLIC_HOSTNAME:? Required env HOOP_PUBLIC_HOSTNAME}"

set -e

mkdir -p /etc/ssl/certs
cp /hoopdata/tls/ca.crt /etc/ssl/certs/ca-certificates.crt

export GRPC_URL=grpc://${HOOP_PUBLIC_HOSTNAME}:80
export API_URL=http://${HOOP_PUBLIC_HOSTNAME}
if [[ "$HOOP_TLS_MODE" == "enabled" ]]; then
    export GRPC_URL=grpcs://${HOOP_PUBLIC_HOSTNAME}:443
    export API_URL=https://${HOOP_PUBLIC_HOSTNAME}
fi

# default idp (Zitadel) setup if IDP_CLIENT_ID is not set
if [ -z "$IDP_CLIENT_ID" ]; then
    export IDP_ISSUER=${API_URL}
    export IDP_CLIENT_ID=$(cat /hoopdata/outputs/default_client_id)
    export IDP_CLIENT_SECRET=$(cat /hoopdata/outputs/default_client_secret)
    export IDP_AUDIENCE=
    export IDP_URI=
    hoop start gateway
    exit $?
fi

# custom idp
hoop start gateway
