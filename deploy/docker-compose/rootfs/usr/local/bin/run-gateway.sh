#!/bin/bash

set -eo pipefail

: "${HOOP_PUBLIC_HOSTNAME:? Required env HOOP_PUBLIC_HOSTNAME}"

mkdir -p /etc/ssl/certs
mkdir -p /hoopdata/outputs
# TODO: should we append this to ca-certificates from the system?
cp /hoopdata/tls/ca.crt /etc/ssl/certs/ca-certificates.crt

export ZITADEL_DATABASE_POSTGRES_HOST=db
export ZITADEL_DATABASE_POSTGRES_PORT=5432
export ZITADEL_DATABASE_POSTGRES_DATABASE=zitadel
export ZITADEL_DATABASE_POSTGRES_USER_USERNAME=zitadel
export ZITADEL_DATABASE_POSTGRES_USER_PASSWORD=zitadel
export ZITADEL_DATABASE_POSTGRES_USER_SSL_MODE=disable
export ZITADEL_DATABASE_POSTGRES_ADMIN_USERNAME=postgres
export ZITADEL_DATABASE_POSTGRES_ADMIN_PASSWORD=postgres
export ZITADEL_DATABASE_POSTGRES_ADMIN_SSL_MODE=disable
export ZITADEL_FIRSTINSTANCE_MACHINEKEYPATH=/hoopdata/zitadel-admin-sa.json
export ZITADEL_FIRSTINSTANCE_ORG_MACHINE_MACHINE_USERNAME=zitadel-admin-sa
export ZITADEL_FIRSTINSTANCE_ORG_MACHINE_MACHINE_NAME=Admin
export ZITADEL_FIRSTINSTANCE_ORG_MACHINE_MACHINEKEY_TYPE=1

export ZITADEL_EXTERNALSECURE=false
export ZITADEL_EXTERNALPORT=80
export ZITADEL_EXTERNALDOMAIN=$HOOP_PUBLIC_HOSTNAME

HEALTHCHECK_ENDPOINT=http://127.0.0.1:80/healthz
TLS_MODE=disabled
if [[ "$HOOP_TLS_MODE" == "enabled" ]]; then
    HEALTHCHECK_ENDPOINT="https://$HOOP_PUBLIC_HOSTNAME/healthz"
    TLS_MODE=external
    export ZITADEL_EXTERNALSECURE=true
    export ZITADEL_EXTERNALPORT=443
fi

zitadel start-from-init --masterkeyFile /hoopdata/zitadel-master.key --port 80 --tlsMode $TLS_MODE &

until curl -k -s -f -o /dev/null "$HEALTHCHECK_ENDPOINT"; do
  sleep 2
done

echo "--> zitadel is running, starting provisioner!"

export TF_VAR_public_hostname=$HOOP_PUBLIC_HOSTNAME
export TF_VAR_tls_mode=$HOOP_TLS_MODE
pushd /opt/terraform
terraform init
terraform apply -auto-approve
terraform output -raw default_client_id > /hoopdata/outputs/default_client_id
terraform output -raw default_client_secret > /hoopdata/outputs/default_client_secret
popd

echo "--> idp provisioner finished, starting gateway!"

export LOG_ENCODING=console
export GIN_MODE=release
export PLUGIN_AUDIT_PATH=/hoopdata/sessions
export PLUGIN_INDEX_PATH=/hoopdata/sessions/indexes
export STATIC_UI_PATH=/opt/hoop/webapp/public
export MIGRATION_PATH_FILES=/opt/hoop/migrations
export POSTGRES_DB_URI=postgres://postgres:postgres@db:5432/hoopdb?sslmode=disable

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
