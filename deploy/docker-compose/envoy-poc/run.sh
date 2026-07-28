#!/usr/bin/env bash
#
# Brings up the Citadel-shaped POC and wires hoop through the API:
#
#   1. compose up (minus envoy -- it needs the OPA tokens first)
#   2. register the admin user (local auth)
#   3. enable the httpproxy + postgres proxy listeners
#   4. create two connections: httpbin (httpproxy) and appdb (postgres)
#   5. attach a guardrail that denies DELETE on the appdb connection
#   6. mint a per-user hoop proxy token for alice, bake it into the Rego
#   7. start envoy
#
# Idempotent-ish: re-running after a `down -v` is the clean path.
#
# Usage:
#   ./run.sh          bring everything up
#   ./run.sh down     tear down including volumes

set -euo pipefail
cd "$(dirname "$0")"

API=http://localhost:8009/api
ADMIN_EMAIL=admin@hoop.dev
ADMIN_PASS=hoop-envoy-poc

# alice is the human whose identity we want to survive all the way to the
# upstream. bob exists only to be denied by the fat gate.
ALICE=alice

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
c_warn() { printf '\033[33m  !!\033[0m  %s\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

if [[ "${1:-}" == "down" ]]; then
    docker compose down -v --remove-orphans
    rm -rf envoy/certs
    exit 0
fi

need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }
need docker; need curl; need jq; need openssl

# --------------------------------------------------------------- 0. TLS cert
# Envoy terminates TLS. Self-signed is fine; every client below uses -k.
c_step "TLS certificate for Envoy"
if [[ ! -f envoy/certs/server.crt ]]; then
    mkdir -p envoy/certs
    openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
        -keyout envoy/certs/server.key -out envoy/certs/server.crt \
        -subj "/CN=localhost" \
        -addext "subjectAltName=DNS:localhost,DNS:envoy,IP:127.0.0.1" \
        2>/dev/null
    c_ok "generated envoy/certs/server.crt"
else
    c_ok "reusing envoy/certs/server.crt"
fi

# ------------------------------------------------------- 1. core services up
# Envoy is held back: its ext_authz cluster fails closed, and OPA has no real
# tokens until step 6.
c_step "Starting upstreams, gateway, agent, opa"
docker compose up -d --wait db appdb httpbin sshd client gateway agent opa
c_ok "gateway healthy on :8009"

# ------------------------------------------------------------ 2. admin login
c_step "Registering admin user"
TOKEN=$(curl -sS -D- -o /dev/null -X POST "$API/localauth/register" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" \
    | awk 'tolower($1)=="token:"{print $2}' | tr -d '\r')

if [[ -z "$TOKEN" ]]; then
    # Already registered (re-run without `down -v`); log in instead.
    TOKEN=$(curl -sS -D- -o /dev/null -X POST "$API/localauth/login" \
        -H 'Content-Type: application/json' \
        -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" \
        | awk 'tolower($1)=="token:"{print $2}' | tr -d '\r')
fi
[[ -n "$TOKEN" ]] || die "could not obtain an admin token"
AUTH=(-H "Authorization: Bearer $TOKEN")
c_ok "admin token acquired"

api() {
    local method=$1 path=$2 body=${3:-}
    if [[ -n "$body" ]]; then
        curl -sS -X "$method" "$API$path" "${AUTH[@]}" \
            -H 'Content-Type: application/json' -d "$body"
    else
        curl -sS -X "$method" "$API$path" "${AUTH[@]}"
    fi
}

# --------------------------------------------------- 3. enable proxy servers
# All three listeners are off by default; they live in server_misc_config and
# the gateway starts/stops them in place (no restart). Omitting hosts_key makes
# the gateway mint an ed25519 host key on first start
# (gateway/api/serverconfig/misc.go:162-171).
c_step "Enabling hoop proxy listeners"
api PUT /serverconfig/misc '{
  "grpc_server_url": "grpc://gateway:8010",
  "product_analytics": "inactive",
  "http_proxy_server_config": { "listen_address": "0.0.0.0:18888" },
  "postgres_server_config":   { "listen_address": "0.0.0.0:15432" },
  "ssh_server_config":        { "listen_address": "0.0.0.0:12222" }
}' | jq -e '.postgres_server_config.listen_address' >/dev/null \
    || die "failed enabling proxy listeners"
c_ok "httpproxy :18888, postgres :15432, ssh :12222"

# ------------------------------------------------------------ 4. connections
AGENT_ID=$(api GET /agents | jq -r '.[0].id')
[[ "$AGENT_ID" != "null" && -n "$AGENT_ID" ]] || die "no agent registered yet"
c_ok "agent $AGENT_ID"

b64() { printf '%s' "$1" | base64 | tr -d '\n'; }

c_step "Creating connections"
api POST /connections "{
  \"name\": \"httpbin\",
  \"type\": \"application\",
  \"subtype\": \"httpproxy\",
  \"agent_id\": \"$AGENT_ID\",
  \"secret\": { \"envvar:REMOTE_URL\": \"$(b64 http://httpbin:8080)\" },
  \"access_mode_runbooks\": \"disabled\",
  \"access_mode_exec\": \"disabled\",
  \"access_mode_connect\": \"enabled\",
  \"access_schema\": \"disabled\"
}" | jq -e '.name' >/dev/null || c_warn "httpbin connection already exists"

api POST /connections "{
  \"name\": \"appdb\",
  \"type\": \"database\",
  \"subtype\": \"postgres\",
  \"agent_id\": \"$AGENT_ID\",
  \"secret\": {
    \"envvar:HOST\": \"$(b64 appdb)\",
    \"envvar:PORT\": \"$(b64 5432)\",
    \"envvar:USER\": \"$(b64 appuser)\",
    \"envvar:PASS\": \"$(b64 apppass)\",
    \"envvar:DB\":   \"$(b64 appdb)\"
  },
  \"access_mode_runbooks\": \"disabled\",
  \"access_mode_exec\": \"enabled\",
  \"access_mode_connect\": \"enabled\",
  \"access_schema\": \"disabled\"
}" | jq -e '.name' >/dev/null || c_warn "appdb connection already exists"

api POST /connections "{
  \"name\": \"appserver\",
  \"type\": \"application\",
  \"subtype\": \"ssh\",
  \"agent_id\": \"$AGENT_ID\",
  \"secret\": {
    \"envvar:HOST\": \"$(b64 sshd)\",
    \"envvar:PORT\": \"$(b64 2222)\",
    \"envvar:USER\": \"$(b64 appops)\",
    \"envvar:PASS\": \"$(b64 appops-pass)\"
  },
  \"access_mode_runbooks\": \"disabled\",
  \"access_mode_exec\": \"disabled\",
  \"access_mode_connect\": \"enabled\",
  \"access_schema\": \"disabled\"
}" | jq -e '.name' >/dev/null || c_warn "appserver connection already exists"
c_ok "httpbin (httpproxy) + appdb (postgres) + appserver (ssh)"

# ------------------------------------------------------------- 5. tier-2 rule
# The thing Envoy structurally cannot do: deny on the CONTENT of the statement.
# OPA never sees this string -- it arrives inside the pgwire stream, after the
# fat gate has already said yes.
c_step "Attaching tier-2 guardrail (deny DELETE on appdb)"
APPDB_ID=$(api GET /connections/appdb | jq -r '.id')
api POST /guardrails "{
  \"name\": \"deny-delete\",
  \"description\": \"tier 2: service owners forbid DELETE on this database\",
  \"input\": { \"rules\": [{
      \"type\": \"deny_words_list\",
      \"words\": [\"DELETE\", \"DROP\", \"TRUNCATE\"],
      \"message\": \"blocked by hoop guardrail: destructive statements are not permitted on appdb\"
  }] },
  \"output\": { \"rules\": [] },
  \"connection_ids\": [\"$APPDB_ID\"]
}" | jq -e '.id' >/dev/null || c_warn "guardrail already exists"
c_ok "DELETE / DROP / TRUNCATE denied on appdb"

# ----------------------------------------------------- 6. per-user credential
# One token per user. This is what Envoy injects, and what makes hoop attribute
# the session to alice rather than to a shared service account.
#
# access_duration_seconds is explicit on purpose: gateway builds before the
# no-expiry sentinel (connection_credentials.go:42) treat an omitted duration
# as "expires now", and the proxy then rejects the token with "credentials
# expired". 24h works on every build.
c_step "Minting hoop proxy token for '$ALICE'"
ALICE_TOKEN=$(api POST /connections/httpbin/credentials '{"access_duration_seconds":86400}' \
    | jq -r '.connection_credentials.proxy_token')
[[ -n "$ALICE_TOKEN" && "$ALICE_TOKEN" != "null" ]] \
    || die "failed minting proxy token"

# Rewrite the token block in the Rego so OPA hands Envoy the real value.
python3 - "$ALICE_TOKEN" <<'PY'
import re, sys
tok = sys.argv[1]
p = "opa/authz.rego"
src = open(p).read()
block = f'''\t# BEGIN-TOKENS
\t"alice": "{tok}",
\t"bob": "",
\t# END-TOKENS'''
src = re.sub(r"\t# BEGIN-TOKENS.*?\t# END-TOKENS", block, src, flags=re.S)
open(p, "w").write(src)
PY
docker compose restart opa >/dev/null
c_ok "token baked into opa/authz.rego (alice allowed, bob denied)"

# ------------------------------------------------------------------ 7. envoy
c_step "Starting Envoy"
docker compose up -d --wait envoy
c_ok "https :8443, postgres :15432, ssh :12222, admin :9901"

cat <<EOF

$(printf '\033[1;32mready\033[0m')

  Tier 1 -- fat gate (Envoy + OPA). Envoy injects alice's hoop token.

    allowed:
      curl -k https://localhost:8443/json -H 'X-Citadel-User: alice'

    denied by OPA (bob has no grant; never reaches hoop):
      curl -k https://localhost:8443/json -H 'X-Citadel-User: bob' -i

    denied by OPA (no identity at all):
      curl -k https://localhost:8443/json -i

  Tier 2 -- deep inspection (hoop). Envoy forwarded raw bytes on both lanes
  below; only hoop can read them. Run ./creds.sh first, then:

    postgres (Envoy :5432 -> hoop :15432):
      docker compose exec -T client env PGPASSWORD=hoop PGSSLMODE=disable \\
        psql -h envoy -p 5432 -U "\$(cat .pgtoken)" -d appdb \\
             -c 'SELECT name, email FROM customers;'

    ssh (Envoy :2222 -> hoop :12222). No connection name on the command
    line -- the credential is already bound to 'appserver':
      docker compose exec -T client sshpass -p "\$(cat .sshtoken)" \\
        ssh -o StrictHostKeyChecking=no -p 2222 hoop@envoy 'whoami; ls /'

    Or just: ./creds.sh && ./demo.sh

  Audit trail -- every request above, attributed to a real user:

      open http://localhost:8009   ($ADMIN_EMAIL / $ADMIN_PASS)

  Logs:
      docker compose logs -f envoy opa gateway

  Teardown:
      ./run.sh down

EOF
