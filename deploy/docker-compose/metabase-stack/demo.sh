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
# of the demo run, so one broken beat does not hide the rest.
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
note "One mask rule for the whole process, spent on the EMAIL_ADDRESS entity."
note "It keys on the value, so email is rewritten under any name a query gives"
note "it. ssn, cpf and iban arrive as stored: sidecar/config.yaml carries a"
note "rule for each, commented out, over the limit. ssn wants two of them --"
note "a column rule for the fixture-shaped values alcatraz declines, an entity"
note "rule for the aliases a column rule misses -- and README 'Known gaps' has"
note "the hole that pair still leaves."

# --------------------------------------------------------------------- export
h "CSV EXPORT / 5,000 rows leaving Metabase entirely"
note "5,000 outruns maxDecodedRows (1,000), which bounds AUDIT decoding only."
note "This is what the one mask rule is spent on: actor_email is a column name"
note "no columns: list predicted, and an entity rule keys on the value."

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
note "One guardrail rule for the whole process, and it is the lane-wide"
note "backstop, so every table answers with the same sentence. The per-table"
note "rule that would name the CRM sync as the owner of customers is commented"
note "out in sidecar/config.yaml, over the limit."
WRITE=$($MB sql 'DELETE FROM customers WHERE id = 1' 2>&1)
printf '%s\n' "$WRITE"
OWNED=$(printf '%s\n' "$WRITE" | grep -c 'read-only')
expect "$OWNED" 1 "the backstop answered for customers"
note ""
note "events was never going to have a rule of its own, and this is a delete"
note "by effect rather than by leading verb:"
BACKSTOP=$($MB sql 'WITH gone AS (DELETE FROM events RETURNING *) SELECT count(*) FROM gone' 2>&1)
printf '%s\n' "$BACKSTOP"
GENERIC=$(printf '%s\n' "$BACKSTOP" | grep -c 'read-only')
expect "$GENERIC" 1 "the backstop answered for the table nobody listed"
note ""
note "A schema change is a write too, and there is no GRANT underneath to"
note "catch it: appuser owns this database."
DDL=$($MB sql 'CREATE TABLE zzz_probe (id int)' 2>&1)
printf '%s\n' "$DDL"
NODDL=$(printf '%s\n' "$DDL" | grep -c 'read-only')
expect "$NODDL" 1 "CREATE is refused, with alter, grant, revoke and call"
note ""
note "Row counts read directly from appdb:"
COUNT=$($PG_DIRECT -tAc 'SELECT count(*) FROM customers;' 2>&1 | tr -d ' \r')
expect "$COUNT" 3 "customers still has 3 rows"
EVENTS=$($PG_DIRECT -tAc 'SELECT count(*) FROM events;' 2>&1 | tr -d ' \r')
expect "$EVENTS" 5000 "events still has 5000 rows"
PROBE=$($PG_DIRECT -tAc "SELECT count(*) FROM pg_tables WHERE tablename = 'zzz_probe';" 2>&1 | tr -d ' \r')
expect "$PROBE" 0 "no zzz_probe table exists, so the statement never landed"

# ---------------------------------------------------------------- table scope
h "TABLE SCOPE / the protection this build cannot afford"
note "payroll-off-limits is a type: table rule, and it is commented out:"
note "one guardrail rule for the whole process, held by read-only-lane."
note "An operation rule cannot deny a SELECT, so reporting reads payroll."
note ""
note "Read directly from appdb, sidecar bypassed:"
$PG_DIRECT -c 'SELECT employee, salary_cents, bank_iban FROM payroll;' 2>&1 | head -8
note ""
note "And through Metabase, which gets the same rows, bank_iban included,"
note "because the iban mask rule is over the limit as well:"
PAYROLL=$($MB sql 'SELECT employee, salary_cents, bank_iban FROM payroll ORDER BY id' 2>&1)
printf '%s\n' "$PAYROLL"
READS=$(printf '%s\n' "$PAYROLL" | grep -c 'Lovelace')
expect "$READS" 1 "payroll is readable in this build, and the demo says so"
note ""
note "That is what the cap costs, and it is the rule to restore first if this"
note "lane is the one you are copying. With payroll-off-limits in force every"
note "path is refused with its own message: the SQL editor, a join where"
note "payroll is not the first table named, and the query builder, because a"
note "table rule reads every relation a statement touches rather than the"
note "first one. Metabase lists the table either way, since sync reads"
note "pg_catalog and no rule covers the catalogue."
note ""
note "Masking cannot stand in for it. A mask rule has no tables: field, so"
note "masking bank_iban would mask customers.iban too, and a salary is not a"
note "value masking has an answer for."

# ---------------------------------------------------------------------- audit
h "AUDIT / every statement Metabase ran, including its own"
note "Nothing in Metabase was configured to log this. The 180s window covers"
note "./run.sh's sync, so counts match the README on the first run only."
sleep 1
docker compose logs hoop-inspect --since 180s 2>/dev/null | ./sidecar/read-audit.py || FAILED=1

h "SIDECAR / counters"
curl -s http://localhost:19000/stats | python3 -m json.tool 2>/dev/null | sed 's/^/  /'

h "Summary"
cat <<'EOF'
  Masked in the query builder, the native SQL editor and a 5,000-row CSV,
  before Metabase received the bytes. Audited per statement, with no agent
  in Metabase and no plugin.

  One guardrail rule and one mask rule for the whole process, so this run
  shows the lane-wide backstop refusing every write and the EMAIL_ADDRESS
  entity rule rewriting every address. The per-table rules and the other
  mask rules are in sidecar/config.yaml, commented, and the beats above
  print what their absence costs instead of claiming they ran.

  The control is on the WAREHOUSE, so the same lane also covers dbt,
  DBeaver, notebooks and psql. Metabase is the client under test here,
  not the boundary being defended.

  Gaps, both in the README: bind parameters escape request-side rules,
  and an aliased column escapes a column rule.
EOF

exit $FAILED
