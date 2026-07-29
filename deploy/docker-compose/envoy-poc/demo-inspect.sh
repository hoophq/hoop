#!/usr/bin/env bash
#
# Exercises the hoop-inspect sidecar end to end.
#
#   client -> Envoy (TLS, OPA fat gate) -> hoop-inspect -> upstream
#
# hoop-inspect is the library as a process: it decodes the wire protocol,
# evaluates policy per statement, writes an audit trail naming the human, and
# masks sensitive values on the way back. Envoy keeps TLS and routing.
#
# Prereqs: ./run.sh (brings the stack up, including hoop-inspect).

set -uo pipefail
cd "$(dirname "$0")"

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
ok()   { printf '\033[32m%s\033[0m\n' "$*"; }

PG="docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable psql -h envoy -p 5433 -U appuser -d appdb"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Run ./run.sh first." >&2
    exit 1
fi

# ---------------------------------------------------------------- postgres
h "POSTGRES / allowed -- SELECT reaches appdb and returns rows"
$PG -c 'SELECT name, email FROM customers;' 2>&1 | head -8

h "POSTGRES / denied -- DELETE never reaches the database"
note "The message below is a real pgwire ErrorResponse, not a dropped socket."
note "The developer reads it in psql and fixes it themselves."
$PG -c 'DELETE FROM customers WHERE id = 1;' 2>&1 | head -2

note ""
note "Proof nothing was deleted:"
$PG -tAc 'SELECT count(*) FROM customers;' 2>&1 | head -1

# -------------------------------------------------------------------- http
h "HTTP / allowed"
curl -sk https://localhost:8444/json -H 'X-Citadel-User: alice' \
    -o /dev/null -w '  HTTP %{http_code}\n'

h "HTTP / denied on the REQUEST -- normalized resource path"
note "One rule covers every id: /anything/users/*/orders/* "
curl -sk https://localhost:8444/anything/users/12345/orders/98765 \
    -H 'X-Citadel-User: alice' -w '\n  HTTP %{http_code}\n'

h "HTTP / denied on the RESPONSE -- the thing ext_authz cannot do"
note "ext_authz decides BEFORE the upstream is called, so it never sees a"
note "status. hoop-inspect sees the 503 and suppresses it."
curl -sk https://localhost:8444/status/503 \
    -H 'X-Citadel-User: alice' -w '\n  HTTP %{http_code}\n'

# ----------------------------------------------------------------- masking
h "MASKING / sensitive values rewritten on the way back"
curl -sk -X POST https://localhost:8444/anything \
    -H 'X-Citadel-User: alice' -H 'Content-Type: application/json' \
    -d '{"email":"ada@example.com","ssn":"555-12-3456","card":"4111111111111111","cpf":"111.444.777-35","iban":"GB82WEST12345698765432"}' \
    2>/dev/null | grep -E '"(email|ssn|card|cpf|iban)"' | sed 's/^/  /' | head -6
note ""
note "One engine detects all five: alcatraz. email redacted; ssn, card and"
note "iban partial-masked keeping the last 4; cpf redacted."
note ""
note "Card, CPF and IBAN are checksum-verified (Luhn, mod-11, ISO 7064), so"
note "order ids and timestamps that merely look like them are left alone."

h "MASKING / what a verified detector refuses to mask"
curl -sk -X POST https://localhost:8444/anything \
    -H 'X-Citadel-User: alice' -H 'Content-Type: application/json' \
    -d '{"real":"555-12-3456","placeholder":"123-45-6789"}' \
    2>/dev/null | grep -E '"(real|placeholder)"' | sed 's/^/  /' | head -2
note ""
note "123-45-6789 is left alone on purpose: alcatraz rejects sequential and"
note "descending digit runs as obvious test fixtures. That is the trade a"
note "validating detector makes - it cuts false positives on ordinary ids and"
note "declines the fixtures. A shop that must mask placeholder SSNs too should"
note "add a pattern rule for them rather than widen the detector."

# -------------------------------------------------------------- guardrails
h "GUARDRAIL / a national id in the query itself"
note "Masking cleans the response. It cannot help with an identifier the"
note "client PUT IN THE QUERY - that text lands in the database's own query"
note "log, slow-query log and EXPLAIN output before any response exists."
note ""
printf '  $ SELECT name FROM customers WHERE cpf = %s...%s\n' "'111.444" "35'"
docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable \
    psql -h envoy -p 5433 -U appuser -d appdb \
    -tAc "SELECT name FROM customers WHERE cpf = '111.444.777-35';" 2>&1 \
    | grep -E 'FATAL|taxpayer' | sed 's/^/  /' | head -2
note ""
printf '  $ SELECT name FROM customers WHERE id = 1\n'
docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable \
    psql -h envoy -p 5433 -U appuser -d appdb \
    -tAc "SELECT name FROM customers WHERE id = 1;" 2>&1 | sed 's/^/  /' | head -2
note ""
note "Same table, same user. The pii rule denied one and allowed the other."

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
docker compose logs hoop-inspect --since 60s 2>/dev/null \
  | ./hoopinspect/read-audit.py

h "SIDECAR / counters"
curl -s http://localhost:19000/stats | python3 -m json.tool 2>/dev/null | sed 's/^/  /'

h "Summary"
cat <<'EOF'
  Envoy saw:        TLS, a user header, a route. It owns the network path.
  OPA saw:          method, path, headers -- the tier-1 reachability gate.
  hoop-inspect saw: the SQL statement, the HTTP resource, the response
                    status, the PII in the body, and which human ran it.

  The postgres DELETE was refused before the database ever received it,
  and the developer got a readable reason in their psql session.
EOF
