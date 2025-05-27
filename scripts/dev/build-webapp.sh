#!/bin/bash

set -eo pipefail

mkdir -p ./dist/dev/resources || true
rm -rf ./dist/dev/resources
rm -f ./webapp/resources/public/js/app.origin.js
cd webapp && npm install && npm run release:hoop-ui && cd ../
cp -a webapp/resources/ ./dist/dev/resources