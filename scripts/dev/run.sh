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
  rm -rf ./dist/dev/resources
  rm -f ./webapp/resources/public/js/app.origin.js
  cd webapp && npm install && npm run release:hoop-ui && cd ../
  cp -a webapp/resources/ ./dist/dev/resources
fi

docker build -t hoopdev -f ./scripts/dev/Dockerfile .
mkdir -p ./dist/dev/bin
cp ./scripts/dev/entrypoint.sh ./dist/dev/bin/entrypoint.sh

VERSION="${VERSION:-unknown}"
CGO_ENABLED=0 GOOS=linux go build \
  -ldflags "-s -w -X github.com/hoophq/hoop/common/version.version=${VERSION} -X github.com/hoophq/hoop/client/proxy.defaultListenAddrValue=0.0.0.0" \
  -o ./dist/dev/bin/hooplinux github.com/hoophq/hoop/client
docker stop hoopdev &> /dev/null || true
docker rm hoopdev &> /dev/null || true

docker run --rm --name hoopdev \
  -p 2225:22 \
  -p 8009:8009 \
  -p 8010:8010 \
  --env-file=.env \
  --cap-add=NET_ADMIN \
  -v ./dist/dev/bin/:/app/bin/ \
  -v ./dist/dev/root/.ssh:/root/.ssh \
  -v ./rootfs/app/migrations/:/app/migrations/ \
  -v ./dist/dev/resources/:/app/ui/ \
  -it hoopdev /app/bin/entrypoint.sh
