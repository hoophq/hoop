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
  echo 'run "make build-dev-webapp" to build the webapp'
  exit 1
fi

docker build -t hoopdev -f ./scripts/dev/Dockerfile .
mkdir -p ./dist/dev/bin
cp ./scripts/dev/entrypoint.sh ./dist/dev/bin/entrypoint.sh

# Build Rust agent for development
echo "Checking for Rust installation..."
if command -v cargo >/dev/null 2>&1; then
    make build-dev-rust
    cp ${HOME}/.hoop/bin/hoop_rs ./dist/dev/bin/hoop_rs
else
    echo "Warning: Rust/cargo not found. Skipping Rust agent build."
    echo "To build the Rust agent, install Rust from https://rustup.rs/"
    echo "Then run: cd agentrs && make build-dev-rust."
fi

VERSION="${VERSION:-unknown}"
CGO_ENABLED=0 GOOS=linux go build \
  -ldflags "-s -w -X github.com/hoophq/hoop/common/version.version=${VERSION} -X github.com/hoophq/hoop/client/proxy.defaultListenAddrValue=0.0.0.0" \
  -o ./dist/dev/bin/hooplinux github.com/hoophq/hoop/client
docker stop hoopdev &> /dev/null || true
docker rm hoopdev &> /dev/null || true

docker run --rm --name hoopdev \
  --network host \
  -p 2225:22 \
  -p 8009:8009 \
  -p 8010:8010 \
  -p 15432:15432 \
  -p 12222:12222 \
  -p 13389:13389 \
  --env-file=.env \
  --cap-add=NET_ADMIN \
  --add-host=host.docker.internal:host-gateway \
  -v ./dist/dev/bin/:/app/bin/ \
  -v ./dist/dev/root/.ssh:/root/.ssh \
  -v ./rootfs/app/migrations/:/app/migrations/ \
  -v ./dist/dev/resources/:/app/ui/ \
  -it hoopdev /app/bin/entrypoint.sh
