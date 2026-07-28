#!/usr/bin/env bash
#
# Mints hoop credentials for the two raw-TCP lanes (postgres + ssh) and writes
# them where demo.sh can find them.
#
# Both proxies authenticate on fields the protocol already has, so a stock
# client needs no hoop plugin:
#
#   postgres  secret key in the USERNAME field, password literal "hoop"
#   ssh       username literal "hoop", secret key as the PASSWORD
#
# See gateway/api/connections/connection_credentials.go:823-839.

set -euo pipefail
cd "$(dirname "$0")"

API=http://localhost:8009/api
ADMIN_EMAIL=admin@hoop.dev
ADMIN_PASS=hoop-envoy-poc

TOKEN=$(curl -sS -D- -o /dev/null -X POST "$API/localauth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" \
    | awk 'tolower($1)=="token:"{print $2}' | tr -d '\r')
[[ -n "$TOKEN" ]] || { echo "login failed -- is the stack up?" >&2; exit 1; }

# access_duration_seconds is explicit: gateway builds before the no-expiry
# sentinel (connection_credentials.go:42) read an omitted duration as
# "expires now" and the proxy then rejects the credential.
mint() {
    curl -sS -X POST "$API/connections/$1/credentials" \
        -H "Authorization: Bearer $TOKEN" \
        -H 'Content-Type: application/json' \
        -d '{"access_duration_seconds":86400}' \
        | jq -r ".connection_credentials.$2"
}

PG_SECRET=$(mint appdb username)
[[ -n "$PG_SECRET" && "$PG_SECRET" != "null" ]] \
    || { echo "failed minting postgres credential" >&2; exit 1; }
printf '%s' "$PG_SECRET" > .pgtoken

SSH_SECRET=$(mint appserver password)
[[ -n "$SSH_SECRET" && "$SSH_SECRET" != "null" ]] \
    || { echo "failed minting ssh credential" >&2; exit 1; }
printf '%s' "$SSH_SECRET" > .sshtoken

cat <<EOF
credentials minted -> .pgtoken, .sshtoken

  postgres, through Envoy:
    docker compose exec -T client \\
      env PGPASSWORD=hoop PGSSLMODE=disable \\
      psql -h envoy -p 5432 -U '$PG_SECRET' -d appdb \\
           -c 'SELECT name, email FROM customers;'

  ssh, through Envoy. Username is the literal "hoop"; the secret is the
  password. The target connection is NOT named on the command line -- the
  credential is already bound to 'appserver' and the proxy reads it from the
  cert/permission extensions (gateway/proxyproto/sshproxy/sshproxy.go:278):
    docker compose exec -T client \\
      sshpass -p '$SSH_SECRET' \\
      ssh -o StrictHostKeyChecking=no -p 2222 hoop@envoy \\
          'whoami; head -1 /etc/os-release'

Every byte above crosses Envoy. Envoy has no pgwire parser and no SSH filter
at all -- both are opaque TCP streams to it. hoop terminates both protocols,
so it records the statement, the command, and the human who ran them.
EOF
