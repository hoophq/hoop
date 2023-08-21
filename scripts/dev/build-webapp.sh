#!/bin/bash
set -eo pipefail

echo "--> STARTING BUILDING WEBAPP AT $HOME/.hoop/dev/webapp ..."

mkdir -p $HOME/.hoop/dev/
rm -rf $HOME/.hoop/dev/webapp
git clone git@github.com:runopsio/webapp.git $HOME/.hoop/dev/webapp
cd $HOME/.hoop/dev/webapp && npm install && npm run release:hoop-ui

echo "--> FINISHED"