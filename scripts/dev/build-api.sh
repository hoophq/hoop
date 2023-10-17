#!/bin/bash
set -eo pipefail

echo "--> STARTING BUILDING WEBAPP AT $HOME/.hoop/dev/api ..."

TEMP_DIR=$(mktemp -d)
mkdir -p $HOME/.hoop/dev/
rm -rf $HOME/.hoop/dev/api
git clone git@github.com:hoophq/api.git $TEMP_DIR
cd $TEMP_DIR && npm install --omit=dev && npm run build && \
    mv node_modules ./out/node_modules && \
    mv ./out/ $HOME/.hoop/dev/api

rm -rf $TEMP_DIR


echo "--> FINISHED"