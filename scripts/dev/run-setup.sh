#!/bin/bash
set -eo pipefail

mkdir -p $HOME/.hoop/dev
FILE="$HOME/.hoop/dev/xtdb-pg.jar"

if [[ -f $FILE ]]; then
  echo "xtdb jar already exists, skipping setup ..."
  exit 0
fi

# check if clojure / java is installed
clojure --version > /dev/null
java --version > /dev/null

TMP_DIR=$(mktemp -d)
git clone git@github.com:hoophq/xtdb.git $TMP_DIR

cd $TMP_DIR && clojure -T:uberjar :uber-file '"'xtdb-pg.jar'"' && \
    mv xtdb-pg.jar $HOME/.hoop/dev/

echo "xtdb jar generate with success at $HOME/.hoop/dev/xtdb-pg.jar"
