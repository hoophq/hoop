#!/usr/bin/env bash
# Sidecar audit rows since $1 (RFC 3339): client direction only.
cd "$(dirname "$0")"
docker compose -f stack/docker-compose.yml logs sidecar --no-log-prefix --since "$1" \
  | jq -c 'select((.kind == "statement" or .kind == "violation") and .direction != "server")
           | {kind, principal, statement, rule, session: .http.headers["x-openai-session-id"]}'
