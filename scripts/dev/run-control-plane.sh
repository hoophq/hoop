#!/bin/bash

set -eo pipefail

# Runs the control plane on the host, against the dev database.
#
# It does not reuse the "make run-dev" container on purpose. That container is
# agent-shaped: entrypoint.sh starts an agent, sshd and an EC2 metadata mock,
# and seeds a default agent row. The control plane opens no gRPC transport, so
# the agent in there would reconnect against nothing forever.
#
# Override PORT, POSTGRES_DB_URI or ENV_FILE from your shell; everything else
# comes from the env file.

PORT="${PORT:-8019}"
ENV_FILE="${ENV_FILE:-.env}"
DB_URI_OVERRIDE="${POSTGRES_DB_URI:-}"

if ! [[ -f $ENV_FILE ]]; then
  echo "missing $ENV_FILE (copy .env.sample)" >&2
  exit 1
fi

# Read the env file the way docker --env-file does: literal KEY=VALUE, no
# expansion. Sourcing it in the shell would break on values that hold spaces or
# braces, such as GOOGLE_APPLICATION_CREDENTIALS_JSON.
while IFS= read -r line || [[ -n $line ]]; do
  [[ $line =~ ^[[:space:]]*(#|$) ]] && continue
  [[ $line != *=* ]] && continue
  export "${line%%=*}=${line#*=}"
done < "$ENV_FILE"

# The env file names the database the way the dev container reaches it. The
# host has no such name, so point at the published port instead.
if [[ -n $DB_URI_OVERRIDE ]]; then
  POSTGRES_DB_URI="$DB_URI_OVERRIDE"
else
  POSTGRES_DB_URI="${POSTGRES_DB_URI//host.docker.internal/127.0.0.1}"
fi
export POSTGRES_DB_URI

# The gateway container may already hold 8009, so serve elsewhere and keep
# API_URL pointing at the port we actually listen on.
export PORT
export API_URL="http://localhost:${PORT}"

# Check the database first. Without it the failure is a long dial error that
# never names the fix.
DB_HOSTPORT="$(printf '%s' "$POSTGRES_DB_URI" | sed -nE 's|^postgres://[^@]*@([^/?]+).*|\1|p')"
if [[ -n $DB_HOSTPORT ]]; then
  DB_HOST="${DB_HOSTPORT%%:*}"
  DB_PORT="${DB_HOSTPORT##*:}"
  [[ $DB_PORT == "$DB_HOST" ]] && DB_PORT=5432
  if ! (exec 3<>"/dev/tcp/${DB_HOST}/${DB_PORT}") 2>/dev/null; then
    echo "no database listening on ${DB_HOST}:${DB_PORT}" >&2
    echo "  start it with: make run-dev-postgres" >&2
    exit 1
  fi
fi

mkdir -p ./dist/dev/bin
echo "--> BUILDING CLIENT ..."
go build -o ./dist/dev/bin/hoop-controlplane github.com/hoophq/hoop/client

echo "--> STARTING CONTROL PLANE ON ${API_URL} ..."
exec ./dist/dev/bin/hoop-controlplane start control-plane
