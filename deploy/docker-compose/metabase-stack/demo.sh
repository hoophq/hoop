#!/usr/bin/env bash
#
# Exercises the stack end to end.
#
#   metabase -> hoop-inspect -> appdb        every path a Metabase user has
#   client   ------------------> appdb        the unmasked ground truth
#
# Every query is issued BY METABASE, through the API its own UI calls
# (./metabase/query.py). psql would be shorter and would prove the wrong
# thing: the claim under test is that redaction survives Metabase's driver,
# result pipeline and download endpoint.
#
# One line per beat. The reasoning lives in ./README.md.
#
# Prereqs: ./run.sh

# No `-e`: an assertion that fails should record the failure and let the rest
# of the demo run, so one broken beat does not hide the other four.
set -uo pipefail
cd "$(dirname "$0")" || exit 1

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
pass() { printf '\033[32m  PASS\033[0m %s\n' "$*"; }
fail() { printf '\033[31m  FAIL\033[0m %s\n' "$*"; FAILED=1; }

FAILED=0

# `[[ ... ]] && pass || fail` reads well and is a trap: the fail branch also
# runs if pass itself returns nonzero. Spelled out instead.
expect() { # expect <got> <want> <description>
    if [[ "$1" == "$2" ]]; then
        pass "$3"
    else
        fail "$3 (expected '$2', got '$1')"
    fi
}

# The client container reaches appdb DIRECTLY on the compose network. Nothing
# it sees is masked, because the sidecar is not in its path.
PG_DIRECT="docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable psql -h appdb -p 5432 -U appuser -d appdb"
MB="python3 ./metabase/query.py"

curl -sf http://localhost:19000/healthz >/dev/null \
    || { echo "hoop-inspect is not healthy. Run ./run.sh first." >&2; exit 1; }
curl -sf http://localhost:3000/api/health >/dev/null \
    || { echo "Metabase is not healthy. Run ./run.sh first." >&2; exit 1; }

# --------------------------------------------------------------- ground truth
h "GROUND TRUTH / what is actually stored"
note "Sidecar bypassed. Masked output only means something next to this."
$PG_DIRECT -c 'SELECT name, email, ssn, cpf, iban FROM customers;' 2>&1 | head -8

# ------------------------------------------------------------- query builder
h "QUERY BUILDER / no SQL typed, and the rows come back masked"
note "From a table click. The one path Pro's column security also covers."
$MB table customers 5

# ----------------------------------------------------------------- SQL editor
h "SQL EDITOR / the path column security cannot cover"
note "Pro's answer here is to switch the editor off. This one stays on."
$MB sql 'SELECT id, name, email, ssn, cpf, iban FROM customers ORDER BY id'
note ""
note "ssn has a column rule AND an entity rule: each misses what the other"
note "catches. README 'Known gaps' has the hole they still leave."

# --------------------------------------------------------------------- export
h "CSV EXPORT / 5,000 rows leaving Metabase entirely"
note "5,000 outruns maxDecodedRows (1,000), which bounds AUDIT decoding only."

CSV=$($MB csv 'SELECT id, actor_email, action FROM events ORDER BY id')
ROWS=$(printf '%s\n' "$CSV" | tail -n +2 | grep -c '.')
REDACTED=$(printf '%s\n' "$CSV" | grep -c 'REDACTED:EMAIL_ADDRESS')
LEAKED=$(printf '%s\n' "$CSV" | grep -c '@example.com')

printf '%s\n' "$CSV" | sed -n '1,3p;4999,5001p' | sed 's/^/  /'
note "  ..."
echo
expect "$ROWS"     5000 "5000 rows exported"
expect "$REDACTED" 5000 "5000 rows redacted, masking did not stop at row 1000"
expect "$LEAKED"   0    "0 addresses survived the export"

# ------------------------------------------------------------------ guardrail
h "GUARDRAIL / a write from the SQL editor never reaches the database"
note "Read-only by policy, not by GRANT, so the message is one an operator wrote."
$MB sql 'DELETE FROM customers WHERE id = 1'
note ""
note "A delete by effect, not by leading verb:"
$MB sql 'WITH gone AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM gone'
note ""
note "Row count read directly from appdb:"
COUNT=$($PG_DIRECT -tAc 'SELECT count(*) FROM customers;' 2>&1 | tr -d ' \r')
expect "$COUNT" 3 "customers still has 3 rows"

# ---------------------------------------------------------------------- audit
h "AUDIT / every statement Metabase ran, including its own"
note "Nothing in Metabase was configured to log this. The 180s window covers"
note "./run.sh's sync, so counts match the README on the first run only."
sleep 1
docker compose logs hoop-inspect --since 180s 2>/dev/null | ./hoopinspect/read-audit.py || FAILED=1

h "SIDECAR / counters"
curl -s http://localhost:19000/stats | python3 -m json.tool 2>/dev/null | sed 's/^/  /'

h "Summary"
cat <<'EOF'
  Masked in the query builder, the native SQL editor and a 5,000-row CSV,
  before Metabase received the bytes. Audited per statement, with no agent
  in Metabase and no plugin.

  The control is on the WAREHOUSE, so the same lane also covers dbt,
  DBeaver, notebooks and psql. Metabase is the client under test here,
  not the boundary being defended.

  Gaps, both in the README: bind parameters escape request-side rules,
  and an aliased column escapes a column rule.
EOF

exit $FAILED
