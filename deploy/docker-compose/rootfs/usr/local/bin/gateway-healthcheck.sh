#!/bin/bash

set -e

PROTO=https
if [ -z $TLS_KEY ]; then
    PROTO=http
fi

curl -k -s $PROTO://127.0.0.1:8009/api/healthz
