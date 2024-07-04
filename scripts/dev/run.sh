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

# Dockerfile with agent tools
cp ./scripts/dev/Dockerfile $HOME/.hoop/dev/Dockerfile

cp ./scripts/dev/entrypoint.sh $HOME/.hoop/dev/entrypoint.sh
cp ./rootfs/usr/local/bin/mysql $HOME/.hoop/dev/mysql
rm -rf $HOME/.hoop/dev/migrations && \
  cp -a ./rootfs/app/migrations $HOME/.hoop/dev/migrations

chmod +x $HOME/.hoop/dev/entrypoint.sh
docker build -t hoopdev -f $HOME/.hoop/dev/Dockerfile $HOME/.hoop/dev/

CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X github.com/runopsio/hoop/common/version.strictTLS=false" -o $HOME/.hoop/dev/hooplinux github.com/runopsio/hoop/client
docker stop hoopdev &> /dev/null || true
docker rm hoopdev &> /dev/null || true
docker run --name hoopdev \
  -p 3001:3001 \
  -p 8008:8008 \
  -p 8009:8009 \
  -p 8010:8010 \
  --env-file=.env \
  --cap-add=NET_ADMIN \
  -v $HOME/.hoop/dev:/app/ \
  -v $HOME/.hoop/dev/webapp/resources:/app/ui/ \
  -v $HOME/.hoop/dev/sessions:/opt/hoop/sessions/ \
  -it hoopdev /app/entrypoint.sh
