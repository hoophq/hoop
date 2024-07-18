#!/bin/bash

set -e

mkdir -p /etc/ssl/certs
cp /hoopdata/tls/ca.crt /etc/ssl/certs/ca-certificates.crt

export IDP_CLIENT_ID=$(cat /hoopdata/outputs/default_client_id)
export IDP_CLIENT_SECRET=$(cat /hoopdata/outputs/default_client_secret)
export IDP_AUDIENCE=
export IDP_URI=
hoop start gateway
