#!/bin/bash

if [[ "$DEBUG_ENTRYPOINT" == "true" ]]; then
    set -x
fi

export GIN_MODE=release
if [[ "$PROFILE" == "dev" ]]; then
    /app/start-dev.sh
    exit $?
fi

# change which API to call in the UI on runtime
if [[ "$API_URL" != "" ]]; then
    sed "s|http://localhost:8009|$API_URL|g" -i app/ui/public/js/app.js
fi

tini -- "${@}"
