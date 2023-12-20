#!/bin/bash

set -eo pipefail

if ! [[ -f .env ]]; then
  echo "missing .env file"
  exit 1
fi

while read -r LINE; do
  if [[ $LINE == *'='* ]] && [[ $LINE != '#'* ]] && [[ $LINE == *"PG_"* ]]; then
    ENV_VAR=$(echo $LINE | envsubst)
    eval export $(echo $ENV_VAR)
  fi
done < .env

# : "${PG_HOST:?Variable not set or empty}"
# : "${PG_DB:?Variable not set or empty}"
# : "${PG_USER:?Variable not set or empty}"
# : "${PG_PASSWORD:?Variable not set or empty}"
# : "${PG_PORT:=5432}"

trap ctrl_c INT

function ctrl_c() {
    docker stop hoopdev && docker rm hoopdev
    exit 130
}

mkdir -p $HOME/.hoop/dev

# Dockerfile with agent tools
cp ./scripts/dev/Dockerfile $HOME/.hoop/dev/Dockerfile

cp ./scripts/dev/entrypoint.sh $HOME/.hoop/dev/entrypoint.sh
rm -rf $HOME/.hoop/dev/migrations && \
  cp -a ./rootfs/app/migrations $HOME/.hoop/dev/migrations

chmod +x $HOME/.hoop/dev/entrypoint.sh
docker build -t hoopdev -f $HOME/.hoop/dev/Dockerfile $HOME/.hoop/dev/

GOOS=linux go build -ldflags "-s -w -X github.com/runopsio/hoop/common/version.strictTLS=false" -o $HOME/.hoop/dev/hooplinux github.com/runopsio/hoop/client
docker stop hoopdev > /dev/null || true
docker run --name hoopdev \
  -p 3001:3001 \
  -p 8008:8008 \
  -p 8009:8009 \
  -p 8010:8010 \
  --env-file=.env \
  -v $HOME/.hoop/dev:/app/ \
  -v $HOME/.hoop/dev/webapp/resources:/app/ui/ \
  -v $HOME/.hoop/dev/sessions:/opt/hoop/sessions/ \
  --rm -it hoopdev /app/entrypoint.sh
