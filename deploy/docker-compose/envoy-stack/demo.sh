#!/usr/bin/env bash
#
# Exercises the stack end to end.
#
#   client -> Envoy (TLS, OPA fat gate) -> hoop-inspect -> upstream
#
# hoop-inspect is the sidecar library as a process: it decodes the wire
# protocol, evaluates policy per statement, writes an audit trail naming the
# human, and masks sensitive values on the way back. Envoy keeps TLS and
# routing.
#
# Prereqs: ./run.sh
#
# Each section below is explained, with the code path behind it, in
# docs/sidecar-flow.md. Run the steps one at a time from the runbook there
# when you want to watch a single lane instead of the whole walk.
#
# Note the ports. The client container reaches Envoy on the compose network,
# where the postgres listener is :5432; from the host that same listener is
# published on :5433.

set -uo pipefail
cd "$(dirname "$0")"

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }

PG="docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable psql -h envoy -p 5432 -U appuser -d appdb"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Run ./run.sh first." >&2
    exit 1
fi

# ------------------------------------------------------------- tier 1 (OPA)
h "TIER 1 / the fat gate answers reachability and nothing else"
note "alice is granted httpbin; bob is not; an anonymous caller has no identity."
printf '  alice     HTTP %s\n' \
    "$(curl -sk https://localhost:8443/json -H 'X-Hoop-User: alice' -o /dev/null -w '%{http_code}')"
printf '  bob       HTTP %s\n' \
    "$(curl -sk https://localhost:8443/json -H 'X-Hoop-User: bob' -o /dev/null -w '%{http_code}')"
printf '  anonymous HTTP %s\n' \
    "$(curl -sk https://localhost:8443/json -o /dev/null -w '%{http_code}')"

# ---------------------------------------------------------------- postgres
h "POSTGRES / allowed -- SELECT reaches appdb and returns rows"
note "Envoy forwarded this as opaque TCP. It has no pgwire parser, so OPA was"
note "never consulted -- there is nothing to consult it about."
$PG -c 'SELECT name, id FROM customers;' 2>&1 | head -8

h "POSTGRES / denied -- a destructive statement never reaches the database"
note "This build enforces ONE guardrail rule for the whole process and spends"
note "it on no-cpf-in-query, so the DELETE below is refused for the taxpayer id"
note "it carries, not for being a DELETE. no-destructive-sql refuses any drop,"
note "delete or truncate on its own; sidecar/config.yaml carries it commented"
note "out, over the limit. The statement here is denied under either rule."
note ""
note "The message below is a real pgwire ErrorResponse, not a dropped socket."
note "The developer reads it in psql and fixes it themselves."
$PG -c "DELETE FROM customers WHERE cpf = '111.444.777-35';" 2>&1 | head -2

note ""
note "Proof nothing was deleted:"
LEFT=$($PG -tAc 'SELECT count(*) FROM customers;' 2>&1 | tr -d '[:space:]')
if [ "$LEFT" = "3" ]; then
    printf '  3 rows, unchanged\n'
else
    printf '\033[1;31m  %s rows -- the guardrail did not hold\033[0m\n' "$LEFT"
fi

h "POSTGRES / masked -- the result set is rewritten on the way back"
note "Postgres rows are length-prefixed binary frames, so this is not byte"
note "substitution: the codec rebuilds every DataRow around the new values."
note "psql never notices that the row it prints is not the row appdb sent."
note ""
note "What it prints is one masked column. This build enforces ONE data masking"
note "rule for the whole process and spends it on emails, so email comes back"
note "redacted while ssn and iban come back in the clear. sidecar/config.yaml"
note "carries the rules that would cover them commented out, over the limit."
$PG -c 'SELECT name, email, ssn, iban FROM customers;' 2>&1 | head -8

# -------------------------------------------------------------------- http
h "HTTP / denied on the REQUEST -- one guardrail, both protocols"
note "no-cpf-in-query is a top-level default, so the rule that refused the"
note "DELETE guards this lane too. It scans the request line, where a taxpayer"
note "id lands in an access log, a Referer header and a trace span."
curl -sk 'https://localhost:8443/anything/customers?cpf=111.444.777-35' \
    -H 'X-Hoop-User: alice' -w '\n  HTTP %{http_code}\n'
note ""
note "Both lanes inherit the wording as well as the verdict, which is why an"
note "HTTP caller is told about a database's logs. Lane-specific wording is a"
note "second rule, and there is no budget for one."

h "HTTP / the rules this build cannot afford"
note "One guardrail rule for the whole process, and no-cpf-in-query holds it."
note "Both requests below are ALLOWED as shipped. sidecar/config.yaml carries"
note "the rule that would refuse each, commented out beside the httpbin lane."
printf '  /anything/users/12345/orders/98765  HTTP %s  no-internal-ids would refuse it\n' \
    "$(curl -sk https://localhost:8443/anything/users/12345/orders/98765 \
        -H 'X-Hoop-User: alice' -o /dev/null -w '%{http_code}')"
printf '  /status/503                         HTTP %s  no-upstream-5xx would suppress it\n' \
    "$(curl -sk https://localhost:8443/status/503 \
        -H 'X-Hoop-User: alice' -o /dev/null -w '%{http_code}')"
note ""
note "no-upstream-5xx is the one to restore first in a demo about Envoy:"
note "ext_authz decides BEFORE the upstream is called, so a response status is"
note "a question it structurally cannot ask. Response-side REWRITING costs no"
note "guardrail budget and still runs -- that is the masking section below."

# ----------------------------------------------------------------- masking
h "MASKING / the one entity this build rewrites"
curl -sk -X POST https://localhost:8443/anything \
    -H 'X-Hoop-User: alice' -H 'Content-Type: application/json' \
    -d '{"email":"ada@example.com","ssn":"555-12-3456","card":"4111111111111111","cpf":"111.444.777-35","iban":"GB82WEST12345698765432"}' \
    2>/dev/null | grep -E '"(email|ssn|card|cpf|iban)"' | sed 's/^/  /' | head -6
note ""
note "Five values in, one rewritten. emails is the whole process's one mask"
note "rule, so email comes back redacted and the other four come back exactly"
note "as posted. sidecar/config.yaml carries the rule for each of them"
note "commented out under the mask block, over the limit:"
note "  ssn   -> ssn    US_SSN,      partial keeping the last 4"
note "  card  -> cards  CREDIT_CARD, partial keeping the last 4"
note "  cpf   -> cpf    BR_CPF,      redact"
note "  iban  -> iban   IBAN_CODE,   partial keeping the last 4"
note ""
note "One engine detects all five: alcatraz. Card, CPF and IBAN are"
note "checksum-verified (Luhn, mod-11, ISO 7064), so order ids and timestamps"
note "that merely look like them are left alone. The same validation makes it"
note "refuse 123-45-6789 as an obvious fixture, and no entity rule can reach a"
note "value the detector declines. That is why the appdb lane's commented mask"
note "override reaches for a columns: [ssn] rule: a column rule masks by"
note "position and does not care what the value looks like."

# -------------------------------------------------------------- guardrails
h "GUARDRAIL / a national id in the query itself"
note "Masking cleans the response. It cannot help with an identifier the"
note "client PUT IN THE QUERY - that text lands in the database's own query"
note "log, slow-query log and EXPLAIN output before any response exists."
note ""
printf '  $ SELECT name FROM customers WHERE cpf = %s...%s\n' "'111.444" "35'"
$PG -tAc "SELECT name FROM customers WHERE cpf = '111.444.777-35';" 2>&1 \
    | grep -E 'FATAL|taxpayer' | sed 's/^/  /' | head -2
note ""
printf '  $ SELECT name FROM customers WHERE id = 1\n'
$PG -tAc "SELECT name FROM customers WHERE id = 1;" 2>&1 | sed 's/^/  /' | head -2
note ""
note "Same table, same user. The pii rule denied one and allowed the other."

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
docker compose logs hoop-inspect --since 60s 2>/dev/null \
  | ./sidecar/read-audit.py

h "SIDECAR / counters"
curl -s http://localhost:19000/stats | python3 -m json.tool 2>/dev/null | sed 's/^/  /'

h "Summary"
cat <<'EOF'
  Envoy saw:        TLS, a user header, a route. It owns the network path.
  OPA saw:          method, path, headers -- the tier-1 reachability gate.
  hoop-inspect saw: the SQL statement, the HTTP resource, the response
                    status, and the PII in both directions.

  The postgres DELETE was refused before the database ever received it,
  and the developer got a readable reason in their psql session.

  Every session above records principal=anonymous: the actor column is
  wired (proxy.Config.IdentityFn) and not yet filled from X-Hoop-User.
  See the Identity section of the README, or docs/sidecar-flow.md for
  the full flow and a per-command runbook.
EOF
