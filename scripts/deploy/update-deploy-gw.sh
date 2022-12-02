#!/bin/bash
set -e

echo "--> modifying gateway deployment"
kubectl apply -f ../../k8s/deployment-gw.yaml
echo "--> modifying services ..."
kubectl apply -f ../../k8s/services.yaml
echo "done!"