#!/bin/bash
set -eo pipefail

echo "--> obtaining jwt secret key ..."
JWT_KEY_ENC=$(aws secretsmanager get-secret-value --secret-id hoop/system/agentcontroller |jq .SecretString -r |jq '.AGENTCONTROLLER_SECRET_KEY' -r | base64)
CHECKSUM_CONFIG=$(uuidgen)
echo "--> deploying agent controller ..."

CONTEXT=$(kubectl config current-context)
if [ "$CONTEXT" != "arn:aws:eks:us-east-2:200074533906:cluster/misc-prod" ]; then
    echo "--> wrong kubernetes context, want=arn:aws:eks:us-east-2:200074533906:cluster/misc-prod, got=$CONTEXT"
    exit 1
fi
sed "s|{{AGENTCONTROLLER_SECRET_KEY}}|$JWT_KEY_ENC|g;s|{{CHECKSUM_CONFIG}}|$CHECKSUM_CONFIG|g" setup.yaml | kubectl apply -f -

echo "--> done"
