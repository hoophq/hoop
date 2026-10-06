#!/usr/bin/env bash
#
# Walks the Oracle lane of the envoy-stack, with a thick and a thin client.
#
#   SQL*Plus (OCI)    ──┐
#                       ├─TNS─> envoy:1521 ──tcp──> hoop-inspect ──TNS──> oracledb
#   python-oracledb ────┘
#
# Prereqs, from the envoy-stack directory:
#   ./run.sh
#   docker compose -f docker-compose.yml -f oracle/docker-compose.oracle.yml up -d --wait
#
# The lane adds no rules of its own. Everything below is the process's ONE
# guardrail rule (no-cpf-in-query) and ONE mask rule (emails), inherited
# from the defaults exactly as the postgres and HTTP lanes inherit them.
#
# Each section checks its own result; the script exits 1 if any check fails.

set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
FAILED=0
ok()   { printf '  \033[32mok\033[0m    %s\n' "$*"; }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$*"; FAILED=1; }

COMPOSE="docker compose --progress quiet -f docker-compose.yml -f oracle/docker-compose.oracle.yml"
# The pluggable database: FREEPDB1 on Oracle Free 23ai, XEPDB1 on XE 21c.
SERVICE="${ORACLE_SERVICE:-FREEPDB1}"
# SQL*Plus settings that print one row per line, columns separated by '|'.
SQLPLUS_SETTINGS="SET PAGESIZE 0
SET FEEDBACK OFF
SET HEADING OFF
SET LINESIZE 200
SET TRIMOUT ON
SET COLSEP '|'
COLUMN name FORMAT A14
COLUMN email FORMAT A26"
sqlplus() {
    printf '%s\n%s\nEXIT\n' "$SQLPLUS_SETTINGS" "$1" |
        $COMPOSE exec -T oracleclient sqlplus -S -L appuser/apppass@//envoy:1521/$SERVICE 2>&1
}
thin() { $COMPOSE exec -T oraclethin python /demo/thin.py "$1" 2>&1; }

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Bring the overlay up first:" >&2
    echo "  ./run.sh && $COMPOSE up -d --wait" >&2
    exit 1
fi

DEMO_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1

# ------------------------------------------------------------------ masked
h "ORACLE / masked -- SQL*Plus, an OCI (thick) client"
note "Envoy forwarded this as opaque TCP; it has no TNS parser, so OPA was"
note "never consulted. The relay read the TTC call, the column describe and"
note "the rows. emails is an ENTITY rule: alcatraz finds the address in each"
note "cell and the codec re-encodes the row around the new length. ssn comes"
note "back in the clear because its rule is over the one-rule limit."
OUT=$(sqlplus 'SELECT id, name, email, ssn FROM customers ORDER BY id;')
printf '%s\n' "$OUT" | grep '|' | sed 's/^/  /'
if [ "$(printf '%s\n' "$OUT" | grep -c 'REDACTED:EMAIL_ADDRESS')" = 3 ] && ! printf '%s' "$OUT" | grep -q '@example.com\|ORA-'; then
    ok "three rows, every email redacted, session exited cleanly"
else
    bad "SQL*Plus result: $OUT"
fi

h "ORACLE / masked -- python-oracledb, a thin client"
note "Thin mode writes TNS/TTC itself, with no Oracle Client libraries: a"
note "different wire encoding of the same calls, decoded by the same codec."
OUT=$(thin select)
printf '%s\n' "$OUT"
if [ "$(printf '%s\n' "$OUT" | grep -c 'REDACTED:EMAIL_ADDRESS')" = 3 ] && ! printf '%s' "$OUT" | grep -q '@example.com\|Error'; then
    ok "three rows, every email redacted"
else
    bad "thin result"
fi
OUT=$(thin bind)
printf '%s\n' "$OUT"
if printf '%s' "$OUT" | grep -q '^  Grace Hopper | \[REDACTED:EMAIL_ADDRESS\]$'; then
    ok "bind-variable lookup, email redacted"
else
    bad "thin bind result"
fi

# ------------------------------------------------------------------- denied
h "ORACLE / denied -- the one guardrail rule, on a third protocol"
note "no-cpf-in-query is a top-level default, so the rule that refused the"
note "postgres DELETE and the HTTP query string refuses this too. The reply"
note "is a native ORA-01031 carrying the operator's message; the relay then"
note "closes that session."
OUT=$(sqlplus "DELETE FROM customers WHERE cpf = '111.444.777-35';")
printf '%s\n' "$OUT" | grep 'ORA-' | head -1 | sed 's/^/  SQL*Plus: /'
if printf '%s' "$OUT" | grep -q 'ORA-01031: do not put a taxpayer id'; then ok "SQL*Plus got ORA-01031"; else bad "SQL*Plus denial: $OUT"; fi
OUT=$(thin denied)
printf '%s\n' "$OUT" | sed 's/^  /  thin:     /'
if printf '%s' "$OUT" | grep -q 'ORA-01031: do not put a taxpayer id'; then ok "python-oracledb got ORA-01031"; else bad "thin denial: $OUT"; fi
note ""
note "Proof nothing was deleted, read straight from oracledb, not through the relay:"
LEFT=$(printf 'SET PAGESIZE 0\nSET FEEDBACK OFF\nSELECT COUNT(*) FROM customers;\nEXIT\n' |
    $COMPOSE exec -T oracleclient sqlplus -S -L appuser/apppass@//oracledb:1521/$SERVICE | tr -d '[:space:]')
if [ "$LEFT" = "3" ]; then
    ok "customers: 3 rows, unchanged"
else
    bad "customers: ${LEFT:-?} rows -- the DELETE reached the server"
fi

# --------------------------------------------------------- session goes on
h "ORACLE / recovered -- a denial ends one session, not the lane"
OUT=$(thin count)
if [ "$OUT" = "3" ]; then
    ok "a new thin session after both denials counts 3 rows"
else
    bad "new session after denial: $OUT"
fi

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null | ./sidecar/read-audit.py || FAILED=1
REFUSED=$($COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null | grep -c '"rule":"stream-unsafe"')
if [ "$REFUSED" = 0 ]; then
    ok "no session was closed for traffic the codec could not read"
else
    bad "$REFUSED session(s) closed as stream-unsafe; see: $COMPOSE logs hoop-inspect"
fi

h "Summary"
cat <<'EOF'
  Same process, same two rules, third protocol. Envoy saw a byte count.
  hoop-inspect masked Oracle result sets for an OCI and a thin client and
  refused an Oracle statement with the rules that already guard appdb and
  httpbin.
EOF
exit "$FAILED"
