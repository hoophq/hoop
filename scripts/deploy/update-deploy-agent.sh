#!/bin/bash
set -e

echo "--> modifying agent deployment"
kubectl apply -f ../../k8s/deployment-agent.yaml
echo "done!"