#!/bin/bash

set -eo pipefail

if ! [[ -f .env ]]; then
  echo "missing .env file"
  exit 1
fi

while read -r LINE; do
  if [[ $LINE == "LIBHOOP"* ]]; then
    ENV_VAR=$(echo $LINE | envsubst)
    eval export $(echo $ENV_VAR)
  fi
done < .env

trap ctrl_c INT

function ctrl_c() {
    docker stop hoopdev
    exit 130
}

LIBHOOP="${LIBHOOP:-_libhoop}"

mkdir -p $HOME/.hoop/dev

# remove symbolic link
rm libhoop || true 2>/dev/null
if [[ $LIBHOOP == "git@"* ]]; then
  rm -rf $HOME/.hoop/dev/libhoop
  git clone $LIBHOOP $HOME/.hoop/dev/libhoop
  rm -rf $HOME/.hoop/dev/libhoop/.git
  ln -s $HOME/.hoop/dev/libhoop libhoop
else
  ln -s $LIBHOOP libhoop
fi

cd libhoop && go mod tidy && cd ../

WEBAPP_BUILD="${WEBAPP_BUILD:-0}"
if [[ $WEBAPP_BUILD == "1" ]]; then
  cd webapp && npm install && npm run release:hoop-ui && cd ../
fi

docker build -t hoopdev -f ./scripts/dev/Dockerfile .
mkdir -p ./dist/dev/

CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X github.com/runopsio/hoop/common/version.strictTLS=false" -o ./dist/dev/hooplinux github.com/runopsio/hoop/client
docker stop hoopdev &> /dev/null || true
docker rm hoopdev &> /dev/null || true

docker run --rm --name hoopdev \
  -p 8008:8008 \
  -p 8009:8009 \
  -p 8010:8010 \
  --env-file=.env \
  --cap-add=NET_ADMIN \
  -v ./scripts/dev/entrypoint.sh:/app/entrypoint.sh \
  -v ./dist/dev/hooplinux:/app/hooplinux \
  -v ./rootfs/app/migrations/:/app/migrations/ \
  -v ./webapp/resources/:/app/ui/ \
  -it hoopdev /app/entrypoint.sh
