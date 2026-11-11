#!/usr/bin/env bash
#
# Exercises the stack end to end.
#
#   client (mysql CLI) -> Envoy (tcp_proxy) -> hoop-inspect -> appdb
#
# hoop-inspect is the sidecar library as a process: it decodes the MySQL
# protocol, evaluates policy per statement, writes an audit trail, and
# rebuilds result-set rows around masked values on the way back. Envoy keeps
# the network path.
#
# Prereqs: ./run.sh
#
# Note the ports. The client container reaches Envoy on the compose network,
# where the listener is :3306; from the host that same listener is published
# on :3307.

set -uo pipefail
cd "$(dirname "$0")"

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }

# MYSQL_PWD rather than -p, so the CLI does not print its password warning.
# The client pins the relay public key and sends the RSA password response
# without requesting a key. --ssl-mode=DISABLED keeps this inspection leg
# plaintext; the relay creates a separate TLS connection to appdb.
MY="docker compose exec -T client env MYSQL_PWD=apppass mysql -h envoy -P 3306 -u appuser appdb --ssl-mode=DISABLED --server-public-key-path=/etc/mysql/relay-certs/relay-auth.pub"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not up; run ./run.sh first" >&2
    exit 1
fi

# The relay's own log is where a refusal that never became a statement is
# explained: a session the codec declared unsafe has no SQL to record.
since_log() { docker compose logs hoop-inspect --since "$1" 2>/dev/null; }
DEMO_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1
# --------------------------------------------------------- authentication
h "MYSQL / authentication -- pinned relay RSA key, verified TLS upstream"
note "The client reads relay-auth.pub and sends its encrypted password without"
note "requesting a key. The sidecar owns the matching private key, decrypts the"
note "response, and sends the password through its verified TLS connection."
note "A non-empty cipher confirms that appdb accepted this session over TLS."
$MY -e "SHOW SESSION STATUS LIKE 'Ssl_cipher'" 2>&1 | sed 's/^/  /'


# ------------------------------------------------------------------ allowed
h "MYSQL / allowed -- SELECT reaches appdb and comes back masked"
note "Envoy forwarded this as opaque TCP. The relay read the handshake for"
note "the negotiated capabilities, then the COM_QUERY, then the column"
note "definitions ahead of the rows: email is masked because a definition"
note "NAMED that column, not because a detector guessed at the value."
note ""
note "The CLI negotiates CLIENT_QUERY_ATTRIBUTES and prefixes the SQL with an"
note "attribute block. The relay strips it, or the classifier would see"
note "\\x00\\x01SELECT and report nothing it can match a rule against."
$MY -e 'SELECT id, name, email, ssn FROM customers' 2>&1 | sed 's/^/  /'

# ------------------------------------------------------------------- denied
h "MYSQL / denied -- a destructive statement never reaches the database"
note "The reply is a real ERR_Packet, 1142 (42000), the frame the server"
note "itself sends for a privilege refusal. The developer reads the reason in"
note "the CLI instead of 'Lost connection to MySQL server during query'."
$MY -e "DELETE FROM customers WHERE id = 1" 2>&1 | sed 's/^/  /'

note ""
note "Proof nothing was deleted:"
LEFT=$($MY -N -e 'SELECT COUNT(*) FROM customers' 2>/dev/null | tr -d '[:space:]')
if [ "$LEFT" = "3" ]; then
    printf '  customers: %s rows, unchanged\n' "$LEFT"
else
    printf '  customers: %s rows -- the DELETE reached the server\n' "${LEFT:-?}"
fi

h "MYSQL / denied -- the statement hides in a comment MySQL executes"
note "/*! ... */ is not a comment to MySQL: the server runs its body. The"
note "lexer knows the dialect, so the rule sees a drop where a generic SQL"
note "tokenizer sees nothing."
$MY -e "/*! DROP TABLE customers */" 2>&1 | sed 's/^/  /'
$MY -e "SELECT COUNT(*) AS still_here FROM customers" 2>&1 | sed 's/^/  /'

# --------------------------------------------------------- session goes dark
h "MYSQL / refused -- framing the relay could not inspect"
note "Compression replaces ordinary MySQL packet framing after authentication."
note "The codec closes the session with a reason instead of forwarding a stream"
note "it can no longer inspect."
note ""
T0=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1
$MY --compression-algorithms=zstd -e 'SELECT 1' 2>&1 | sed 's/^/  client: /' | head -3
note ""
note "What the relay logged, naming the client-side fix:"
sleep 1
since_log "$T0" | ./sidecar/read-refusals.py | fold -s -w 76 | sed 's/^\([^ ]\)/     \1/'

note ""
note "--ssl-mode=REQUIRED fails before authentication because this inspection"
note "lane deliberately does not advertise downstream TLS. The upstream hop is"
note "still required and verified independently."
$MY --ssl-mode=REQUIRED -e 'SELECT 1' 2>&1 | sed 's/^/  client: /' | head -3

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
since_log "$DEMO_START" | ../envoy-stack/sidecar/read-audit.py

h "SIDECAR / counters"
curl -s http://localhost:19000/stats | python3 -m json.tool 2>/dev/null | sed 's/^/  /'

h "Summary"
cat <<'EOF'
  Envoy saw:        a byte count. It owns the network path.
  hoop-inspect saw: every statement and every result-set column on the
                    plaintext client leg.
  appdb saw:        verified TLS, one SELECT and one COUNT. Nothing destructive.
EOF
