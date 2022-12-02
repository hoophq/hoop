#!/bin/bash
set -e

echo "--> modifying ingresses"
kubectl apply -f ../../k8s/ingresses.yaml
echo "done!"