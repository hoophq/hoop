#!/bin/bash
set -e

PREFIX=hoop
SECRET_FILE=hoop-config-secret.yaml
echo ">>> updating $PREFIX/$SECRET_FILE"
echo "--> downloading secret $PREFIX/${SECRET_FILE} from AWS"
aws secretsmanager get-secret-value \
    --secret-id $PREFIX/$SECRET_FILE |jq .SecretString -r | kubectl apply -f -
echo "done!"

SECRET_FILE=xtdb-config.yaml
echo ">>> updating $PREFIX/$SECRET_FILE"
echo "--> downloading secret $PREFIX/${SECRET_FILE} from AWS"
aws secretsmanager get-secret-value \
    --secret-id $PREFIX/$SECRET_FILE |jq .SecretString -r | kubectl apply -f -
echo "done!"
