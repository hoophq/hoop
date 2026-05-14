#!/bin/bash

set -eo pipefail

mkdir -p ./dist/dev/resources || true
rm -rf ./dist/dev/resources
rm -f ./webapp/resources/public/js/app.origin.js
# Build CLJS bundle
cd webapp && npm install && npm run release:hoop-ui && cd ../
# Build React shell
cd webapp_v2 && npm install && npm run build && cd ../
# Merge: CLJS resources first, React shell on top (React's index.html wins)
cp -a webapp/resources/ ./dist/dev/resources
cp -a webapp_v2/dist/. ./dist/dev/resources/public/