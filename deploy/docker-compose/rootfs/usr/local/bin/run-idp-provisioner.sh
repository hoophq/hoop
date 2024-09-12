#!/bin/bash

set -e

mkdir -p /etc/ssl/certs
cp /hoopdata/tls/ca.crt /etc/ssl/certs/ca-certificates.crt

mkdir -p /hoopdata/outputs

cd /opt/terraform
terraform init
terraform apply -auto-approve
terraform output -raw default_client_id > /hoopdata/outputs/default_client_id
terraform output -raw default_client_secret > /hoopdata/outputs/default_client_secret
