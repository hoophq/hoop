#!/usr/bin/env bash
#
# Walks the MySQL lane of the envoy-stack.
#
#   mysqlclient ──plaintext──> envoy:3306 ──tcp──> hoop-inspect ──tcp──> mysqldb
#
# Prereqs, from the envoy-stack directory:
#   ./run.sh
#   docker compose -f docker-compose.yml -f mysql/docker-compose.mysql.yml up -d --wait
#
# The lane adds no rules of its own. Everything below is the process's ONE
# guardrail rule (no-cpf-in-query) and ONE mask rule (emails), inherited
# from the defaults exactly as the postgres and HTTP lanes inherit them.

set -uo pipefail
cd "$(dirname "$0")/.."

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }

COMPOSE="docker compose --progress quiet -f docker-compose.yml -f mysql/docker-compose.mysql.yml"
# MYSQL_PWD rather than -p, so the CLI does not print its password warning.
# PREFERRED sees CLIENT_SSL removed from the relay's downstream greeting; the
# relay creates a separate verified TLS connection to mysqldb.
MY="$COMPOSE exec -T mysqlclient env MYSQL_PWD=apppass mysql -h envoy -P 3306 -u appuser appdb"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Bring the overlay up first:" >&2
    echo "  ./run.sh && $COMPOSE up -d --wait" >&2
    exit 1
fi

DEMO_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1
# ------------------------------------------------------------- encrypted hop
h "MYSQL / transport -- plaintext client, verified TLS upstream"
note "The client-facing greeting has CLIENT_SSL removed, so the sidecar can"
note "inspect ordinary frames. This status belongs to the database connection"
note "on the other leg; a non-empty cipher proves mysqldb accepted it over TLS."
$MY -e "SHOW SESSION STATUS LIKE 'Ssl_cipher'" 2>&1 | sed 's/^/  /'


# ------------------------------------------------------------------ masked
h "MYSQL / masked -- the one mask rule, on a third protocol"
note "Envoy forwarded this as opaque TCP; it has no MySQL parser, so OPA was"
note "never consulted. The relay read the handshake for the negotiated"
note "capabilities, the COM_QUERY, then the column definitions and rows."
note "emails is an ENTITY rule: alcatraz finds the address in each cell, and"
note "the codec rebuilds the row around the new length. ssn comes back in"
note "the clear because the rule that would cover it is over the one-rule"
note "limit (see ../sidecar/config.yaml)."
$MY -e 'SELECT id, name, email, ssn FROM customers' 2>&1 | sed 's/^/  /'

# ------------------------------------------------------------------- denied
h "MYSQL / denied -- the one guardrail rule, on a third protocol"
note "no-cpf-in-query is a top-level default, so the rule that refused the"
note "postgres DELETE and the HTTP query string refuses this too. The reply"
note "is a real ERR_Packet, 1142 (42000); the developer reads the reason in"
note "the CLI instead of 'Lost connection to MySQL server during query'."
$MY -e "DELETE FROM customers WHERE cpf = '111.444.777-35'" 2>&1 | sed 's/^/  /'
note ""
note "Proof nothing was deleted:"
LEFT=$($MY -N -e 'SELECT COUNT(*) FROM customers' 2>/dev/null | tr -d '[:space:]')
if [ "$LEFT" = "3" ]; then
    printf '  customers: %s rows, unchanged\n' "$LEFT"
else
    printf '  customers: %s rows -- the DELETE reached the server\n' "${LEFT:-?}"
fi
note ""
note "A plain DELETE ... WHERE id = 1 would reach the database as this file"
note "ships: no-destructive-sql is over the one-rule limit. ../mysql-stack"
note "spends its single rule on that one instead."

# --------------------------------------------------------- session goes dark
h "MYSQL / refused -- framing the relay could not inspect"
note "Compression replaces ordinary MySQL packet framing after authentication."
note "The codec closes the session with a reason instead of forwarding a stream"
note "it can no longer inspect."
T0=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1
$MY --compression-algorithms=zstd -e 'SELECT 1' 2>&1 | sed 's/^/  client: /' | head -3
note ""
note "What the relay logged, naming the client-side fix:"
sleep 1
$COMPOSE logs hoop-inspect --since "$T0" 2>/dev/null \
  | ../mysql-stack/sidecar/read-refusals.py | fold -s -w 76 | sed 's/^\([^ ]\)/     \1/'
note ""
note "--ssl-mode=REQUIRED fails before authentication because this inspection"
note "lane deliberately does not advertise downstream TLS. The upstream hop is"
note "still required and verified independently."
$MY --ssl-mode=REQUIRED -e 'SELECT 1' 2>&1 | sed 's/^/  client: /' | head -3

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null | ./sidecar/read-audit.py

h "Summary"
cat <<'EOF'
  Same process, same two rules, third protocol. Envoy saw a byte count.
  hoop-inspect masked a MySQL result set and refused a MySQL statement with
  the rules that already guard appdb and httpbin. mysqldb required TLS on
  the independently verified upstream hop.
EOF
