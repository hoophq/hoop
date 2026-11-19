#!/usr/bin/env bash
#
# Walks the four ClickHouse lanes of the envoy-stack.
#
#   client   ──TLS──> envoy:8446 ──OPA──> hoop-inspect ──http──> clickhouse:8123
#   chclient ──tcp──> envoy:9000 ───────> hoop-inspect ──tcp───> clickhouse:9000
#   client   ──tcp──> envoy:9004 ───────> hoop-inspect ──tcp───> clickhouse:9004
#   client   ──tcp──> envoy:9005 ───────> hoop-inspect ──tcp───> clickhouse:9005
#
# Prereqs, from the envoy-stack directory:
#   ./run.sh
#   docker compose -f docker-compose.yml -f clickhouse/docker-compose.clickhouse.yml up -d --wait
#
# Every lane inherits the process's one guardrail rule and one mask rule.
# The HTTP lane reads the SQL in ClickHouse's query parameter and body.

set -uo pipefail
cd "$(dirname "$0")/.."

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }

COMPOSE="docker compose --progress quiet -f docker-compose.yml -f clickhouse/docker-compose.clickhouse.yml"
# The MariaDB CLI on the mysql lane, not the MySQL 8 one; the last beat
# explains. --skip-ssl because the relay refuses a client-initiated TLS
# upgrade by design (see ../mysql/README.md).
MY="$COMPOSE exec -T client env MYSQL_PWD=apppass mariadb -h envoy -P 9004 -u appuser --skip-ssl appdb"
PG="$COMPOSE exec -T client env PGPASSWORD=apppass psql -h envoy -p 9005 -U appuser -d appdb"
CH="$COMPOSE exec -T chclient clickhouse-client --host envoy --port 9000 --user appuser --password apppass --database appdb"
# -k: Envoy's cert is self-signed. X-ClickHouse-Key is the database
# password; the lane's header allowlist keeps it out of the audit trail.
CURL="$COMPOSE exec -T client curl -sk https://envoy:8446/ -H X-ClickHouse-User:appuser -H X-ClickHouse-Key:apppass"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Bring the overlay up first:" >&2
    echo "  ./run.sh && $COMPOSE up -d --wait" >&2
    exit 1
fi

DEMO_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1

# ------------------------------------------------------------ mysql lane
h "MYSQL EMULATION :9004 / masked -- the one mask rule"
note "ClickHouse answers on :9004 as if it were MySQL. Envoy forwarded opaque"
note "TCP; the relay read the handshake, the COM_QUERY, the column definitions"
note "and the rows, and rebuilt each row around the redacted cell."
$MY -e 'SELECT id, name, email, ssn FROM customers' 2>&1 | sed 's/^/  /'

h "MYSQL EMULATION :9004 / denied -- the one guardrail rule"
note "Refused at the relay as a real ERR_Packet, 1142 (42000). ClickHouse"
note "never saw the statement."
$MY -e "DELETE FROM customers WHERE cpf = '111.444.777-35'" 2>&1 | grep -v '^-\{5,\}$' | sed 's/^/  /'

# --------------------------------------------------------- postgres lane
h "POSTGRESQL EMULATION :9005 / masked"
note "Same server, same table, pgwire this time. The same emails rule"
note "rewrites the DataRow."
$PG -c 'SELECT id, name, email, ssn FROM customers' 2>&1 | sed 's/^/  /'

h "POSTGRESQL EMULATION :9005 / denied"
$PG -tAc "DELETE FROM customers WHERE cpf = '111.444.777-35'" 2>&1 \
    | grep -E 'FATAL|taxpayer' | sed 's/^/  /' | head -2

note ""
note "Proof nothing was deleted, over the pg lane:"
LEFT=$($PG -tAc 'SELECT count() FROM customers' 2>/dev/null | tr -d '[:space:]')
if [ "$LEFT" = "3" ]; then
    printf '  customers: %s rows, unchanged\n' "$LEFT"
else
    printf '  customers: %s rows -- a DELETE reached the server\n' "${LEFT:-?}"
fi

# ------------------------------------------------------------- http lane
h "HTTP :8446 / tier 1 -- OPA's fat gate, before any SQL is read"
note "bob has no grant on \"clickhouse\" (../opa/authz.rego keys the service"
note "on Envoy's listener port, 8446). hoop-inspect never saw the request."
$CURL -H 'X-Hoop-User: bob' -w ' [%{http_code}]\n' --data-binary 'SELECT 1' 2>&1 | sed 's/^/  /'

h "HTTP :8446 / tier 2 -- SQL judged, result masked"
note "alice is granted. The SQL rides in the POST body; http.sql reads it into"
note "a statement of its own, so the guardrail and the trail see the SQL. The"
note "result set is masked: ClickHouse chunks every response and the codec"
note "re-chunks what it rewrote, one JSONEachRow row at a time, each value"
note "under its column name."
$CURL -H 'X-Hoop-User: alice' --data-binary 'SELECT id, name, email FROM appdb.customers FORMAT JSONEachRow' 2>&1 | sed 's/^/  /'
note ""
note "The rule that refused the pgwire DELETE reads that SQL too. A SELECT"
note "carrying the taxpayer id in the body is refused by the relay; without"
note "http.sql the lane saw only POST / and returned the row:"
$CURL -H 'X-Hoop-User: alice' -w ' [%{http_code}]\n' \
    --data-binary "SELECT name FROM appdb.customers WHERE cpf = '111.444.777-35'" 2>&1 | sed 's/^/  /'
note ""
note "ClickHouse joins the query parameter and the body with a line feed, and"
note "the relay reads them the same way. The id split across both is refused:"
$COMPOSE exec -T client curl -sk 'https://envoy:8446/?query=SELECT%20name%20FROM%20appdb.customers%20WHERE' \
    -H X-ClickHouse-User:appuser -H X-ClickHouse-Key:apppass -H 'X-Hoop-User: alice' \
    -w ' [%{http_code}]\n' --data-binary "cpf = '111.444.777-35'" 2>&1 | sed 's/^/  /'
note ""
note "A client that negotiates a compression the codec cannot undo would be"
note "the one unmasked response, so the relay refuses it instead:"
$COMPOSE exec -T client curl -sk 'https://envoy:8446/?enable_http_compression=1' \
    -H X-ClickHouse-User:appuser -H X-ClickHouse-Key:apppass -H 'X-Hoop-User: alice' -H 'Accept-Encoding: zstd' \
    -w ' [%{http_code}]\n' --data-binary 'SELECT email FROM appdb.customers FORMAT TSV' 2>&1 | sed 's/^/  /'
note ""
note "The request line is scanned too. ClickHouse binds ?param_x= into"
note "{x:Type} in the SQL, so a taxpayer id passed that way sits on the line,"
note "not in the SQL, and the same rule refuses it there. The 403 is the relay's:"
$COMPOSE exec -T client curl -sk -G https://envoy:8446/ \
    -H X-ClickHouse-User:appuser -H X-ClickHouse-Key:apppass -H 'X-Hoop-User: alice' \
    -w ' [%{http_code}]\n' --data-urlencode 'param_cpf=111.444.777-35' \
    --data-urlencode 'query=SELECT name FROM appdb.customers WHERE cpf = {cpf:String}' 2>&1 | sed 's/^/  /'

# ---------------------------------------------------------- native lane
h "NATIVE :9000 / compressed result masked"
note "clickhouse-client negotiated the pinned native revision and LZ4 result"
note "compression. The relay inflated one bounded block at a time and rebuilt"
note "each block around the masked email; it never accumulated the result set."
$CH --query 'SELECT id, name, email, ssn FROM customers ORDER BY id' 2>&1 | sed 's/^/  /'

h "NATIVE :9000 / denied with a native Exception packet"
note "The same taxpayer-id guardrail fires on a native Query packet. Error 497"
note "is synthesized by the relay; ClickHouse never receives the DELETE."
$CH --query "DELETE FROM customers WHERE cpf = '111.444.777-35'" 2>&1 | sed 's/^/  /'

# ------------------------------------------------------------ codec gap
h "MYSQL EMULATION :9004 / known gap -- the MySQL 8 CLI"
note "Every beat above used the MariaDB CLI. The official MySQL 8.0.23+ CLI"
note "sets CLIENT_QUERY_ATTRIBUTES whether or not the server offered it, and"
note "ClickHouse does not. The client then sends COM_QUERY with no attribute"
note "block; the codec, reading the flags from the client's response alone,"
note "looks for one, finds the bytes malformed, and forwards a statement it"
note "could not read. A SELECT carrying the taxpayer id the lane refused above:"
$COMPOSE exec -T mysqlclient env MYSQL_PWD=apppass mysql -h envoy -P 9004 -u appuser --ssl-mode=DISABLED appdb \
    -e "SELECT name FROM customers WHERE cpf = '111.444.777-35'" 2>&1 | sed 's/^/  /'
note ""
note "What the relay recorded for it:"
sleep 1
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null \
    | grep -o '"sql.incomplete":"[^"]*"' | sort -u | sed 's/^/  /'
note ""
note "The fix is in libhoop's mysql codec (intersect the client's flags with"
note "the server greeting's), not in this stack. Until then a lane fronting"
note "any server without CLIENT_QUERY_ATTRIBUTES -- ClickHouse, MariaDB,"
note "MySQL before 8.0.23 -- is enforced only for clients that do not set it."

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
note "The HTTP lane captures request bodies on its SQL routes and no response"
note "body, so the trail holds the SQL and none of the result set. The mysql"
note "and pg lanes leak nothing either: their audit rows carry columns and"
note "counts, not values."
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null | ./sidecar/read-audit.py

h "Summary"
cat <<'EOF'
  One ClickHouse, four front doors, one process. All four lanes masked the
  same email column; the native lane did so over bounded, block-at-a-time
  LZ4 decoding, the HTTP lane by re-chunking JSONEachRow. All four enforced
  the SQL rule: the HTTP lane reads the SQL in ClickHouse's query parameter
  and body. The MySQL 8 CLI beat still names the emulation codec gap.
EOF
