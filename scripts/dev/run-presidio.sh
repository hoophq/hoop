#!/bin/bash

set -eo pipefail

echo -e "--> STARTING PRESIDIO DEV SERVER ...\n"

docker stop panalyzer 2&> /dev/null || true
docker rm panalyzer 2&> /dev/null || true

docker stop panom 2&> /dev/null || true
docker rm panom 2&> /dev/null || true

docker run --rm -d --name panalyzer -p 5002:3000 mcr.microsoft.com/presidio-analyzer
docker run --rm -d --name panom -p 5001:3000 mcr.microsoft.com/presidio-anonymizer


until curl -s -f -o /dev/null "http://127.0.0.1:5002/health"
do
  sleep 1
done

until curl -s -f -o /dev/null "http://127.0.0.1:5001/health"
do
  sleep 1
done

echo -e "\n--> PRESIDIO ANALYZER (0.0.0.0:5002) AND ANONYMIZER (0.0.0.0:5001) ARE READY!"