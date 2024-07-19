#!/bin/bash

set -e

mkdir -p /etc/ssl/certs
cp /hoopdata/tls/ca.crt /etc/ssl/certs/ca-certificates.crt

# default idp (Zitadel) setup if IDP_CLIENT_ID is not set
if [ -z "$IDP_CLIENT_ID" ]; then
    export IDP_ISSUER=https://${HOOP_PUBLIC_HOSTNAME}
    export IDP_CLIENT_ID=$(cat /hoopdata/outputs/default_client_id)
    export IDP_CLIENT_SECRET=$(cat /hoopdata/outputs/default_client_secret)
    export IDP_AUDIENCE=
    export IDP_URI=
    hoop start gateway
    exit $?
fi

# custom idp
hoop start gateway
