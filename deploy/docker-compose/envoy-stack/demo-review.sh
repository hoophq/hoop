#!/usr/bin/env bash
#
# Walks the human-approval gate end to end.
#
#   agent ──sql──> envoy ──> hoop-inspect ──> appdb        (data path)
#     │                          │
#     │                          └──HTTPS──> hoop-gateway  (claim / file)
#     └──HTTPS poll ────────────────────────> hoop-gateway
#                                                  ▲
#   human ─────────────── approves ────────────────┘
#
# The thing to watch: NOTHING is ever held open. A flagged statement is
# refused and the database session ends. The agent polls the gateway, waits
# for a person, reconnects, and re-issues. That is what makes the feature
# implementable at all — holding a pgwire connection for a human needs a
# cancellation the policy interface cannot carry, and would trip every
# idle timeout between here and the database.
#
# Prereqs: ./run.sh --review
#
set -uo pipefail
cd "$(dirname "$0")"

GW="http://localhost:8009/api"
PG="docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable psql -h envoy -p 5432 -U appuser -d appdb"

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
bad()  { printf '\033[31m%s\033[0m\n' "$*"; }
good() { printf '\033[32m%s\033[0m\n' "$*"; }

curl -sf http://localhost:19000/healthz >/dev/null \
    || { bad "hoop-inspect is not healthy. Run ./run.sh --review first."; exit 1; }
curl -sf "$GW/healthz" >/dev/null \
    || { bad "the gateway is not up. Run ./run.sh --review (not plain ./run.sh)."; exit 1; }

# The two credentials this walk needs, written by review-bootstrap. The
# sandbox token polls; the admin token approves. In a real deployment these
# are two different parties, which is the entire point of the feature.
read_secret() {
    docker compose --profile review --profile review-bootstrap run --rm --no-deps -T \
        --entrypoint /bin/sh review-bootstrap -c "cat /secrets/$1" 2>/dev/null | tr -d '\r\n'
}
SANDBOX_TOKEN=$(read_secret review-token)
ADMIN_TOKEN=$(read_secret admin-token)
[ -n "$SANDBOX_TOKEN" ] || { bad "no sandbox token; re-run ./run.sh --review"; exit 1; }

# The statement the agent wants to run, and the marker that says which of its
# tasks this is. The marker is REQUEST identity only: it decides whether a
# retry files a duplicate review, never what an approval permits.
STMT="DELETE FROM customers WHERE id = 1"
MARKER="demo-task-1"
MARKED="-- hoopdev:correlation_id=${MARKER}
${STMT};"

# The authorization key. Exact, and computed from the statement text with
# hoop's own marker removed and the ends trimmed -- nothing else. No
# lowercasing, no whitespace collapsing: each of those would merge two
# statements a person approved separately.
#
# Note the missing semicolon. The codec's splitter already consumed it, so the
# text the gate hashes ends at the final character of the statement. Nobody
# should have to know that, which is why the denial below tells you the hash.
HASH=$(printf '%s' "$STMT" | shasum -a 256 2>/dev/null | cut -d' ' -f1)
[ -n "$HASH" ] || HASH=$(printf '%s' "$STMT" | sha256sum | cut -d' ' -f1)

sandbox() { curl -sS -H "Authorization: Bearer $SANDBOX_TOKEN" "$@"; }
admin()   { curl -sS -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' "$@"; }
poll()    { sandbox "$GW/relay/reviews?connection=appdb&statement_hash=$HASH"; }

# ---------------------------------------------------------------- 0. baseline
h "0 / an ordinary read is untouched"
note "The trigger names delete and update, so a SELECT is never classified,"
note "never costs a model call, and never costs a gateway round trip."
$PG -c 'SELECT name, email FROM customers ORDER BY id LIMIT 3;' 2>&1 | sed 's/^/  /'

# ------------------------------------------------------------- 1. first issue
h "1 / the agent issues a DELETE -- refused, and a review is filed"
note "hoop-inspect classifies it, maps the verdict to require_review, asks the"
note "gateway for an existing approval (there is none), files one, and kills"
note "the session. Severity FATAL: the connection is going away, and reporting"
note "ERROR would leave psql waiting for a ReadyForQuery that never comes."
echo
$PG -c "$MARKED" 2>&1 | sed 's/^/  /'

# ------------------------------------------------------------------ 2. status
h "2 / the agent polls the control plane"
note "GET /api/relay/reviews with its own hpk_ token. Read-only: polling can"
note "never consume an approval, which only the relay's claim may do."
echo
poll | python3 -m json.tool 2>/dev/null | sed 's/^/  /' || poll | sed 's/^/  /'

REVIEW_ID=$(poll | python3 -c 'import json,sys; print(json.load(sys.stdin).get("review_id",""))' 2>/dev/null)
if [ -z "$REVIEW_ID" ]; then
    bad "no review was filed -- check: docker compose logs hoop-inspect"
    exit 1
fi

# ------------------------------------------------------- 3. duplicate control
h "3 / the agent retries before a human has looked -- no duplicate review"
note "The claim filters on APPROVED, so a retry does not see its own PENDING"
note "review. Without a separate dedupe key a polling agent would file one per"
note "attempt; the create path matches on the MARKER instead."
echo
$PG -c "$MARKED" >/dev/null 2>&1
COUNT=$(admin "$GW/reviews" | python3 -c '
import json,sys
print(sum(1 for r in json.load(sys.stdin) if r.get("status") == "PENDING"))' 2>/dev/null)
printf '  PENDING reviews after two attempts: %s\n' "$COUNT"

# ------------------------------------------------------------------ 4. human
h "4 / a human approves it"
note "The org's existing review apparatus: groups, min_approvals, the webapp,"
note "Slack. No second approval system was built for this."
echo
note "  reviewer: admin@hoop.dev / hoopdemo123   (webapp at http://localhost:8009)"
admin -X PUT "$GW/reviews/$REVIEW_ID" -d '{"status":"APPROVED"}' >/dev/null
printf '  approved review %s\n' "$REVIEW_ID"
echo
poll | python3 -m json.tool 2>/dev/null | sed 's/^/  /'

# ------------------------------------------------------------------ 5. retry
h "5 / the agent reconnects and re-issues the SAME statement"
note "A new connection, because the old one is gone. The claim hits before"
note "classification, so this costs one gateway round trip and no model call."
echo
$PG -c "$MARKED" 2>&1 | sed 's/^/  /'

# -------------------------------------------------------------- 6. single use
h "6 / it runs exactly once"
note "The approval was consumed by the claim, in the same UPDATE that found"
note "it. A second attempt finds no APPROVED row and takes the same terminal"
note "denial -- a human approved one execution of this statement, not a"
note "standing permission."
echo
$PG -c "$MARKED" 2>&1 | sed 's/^/  /'

# ------------------------------------------------------- 7. the wrong statement
h "7 / an approval never covers a different statement"
note "This is the threat the exact hash exists for. Approve"
note "\"WHERE id = 1\" and a shape-keyed lookup would authorize"
note "\"WHERE id = 999\" -- every row, on one signature. It does not."
echo
$PG -c "-- hoopdev:correlation_id=demo-task-2
DELETE FROM customers WHERE id = 999;" 2>&1 | sed 's/^/  /'

# ------------------------------------------------------ 8. what is NOT gated
h "8 / two shapes the gate refuses rather than holds"
note "A statement whose parameter values the relay cannot read is refused, not"
note "gated: one approval would otherwise cover every later binding. psql -c"
note "sends a simple Query, so this lane cannot show a driver's Parse/Bind"
note "from here -- hoopinspect/review covers it in TestOnlyLiteralStatements-"
note "AreObservable, and the same refusal applies to mssql sp_executesql."
note ""
note "What IS worth seeing: EXECUTE decides its effect at runtime, so the lexer"
note "reports \"unknown\" rather than guessing. This lane names unknown in the"
note "trigger, so it is classified and held like anything else. Leave it out"
note "and PREPARE ... AS DELETE / EXECUTE walks straight past the gate."
echo
$PG -c "EXECUTE nonexistent_plan(2);" 2>&1 | head -2 | sed 's/^/  /'

# ------------------------------------------------------------------ 9. trail
h "9 / what the audit trail kept"
note "review_status says what the gate did on each statement; ai_status reads"
note "skipped on the approved retry, because an approved statement is not"
note "reclassified. Neither channel ever holds a value the statement carried."
echo
curl -s localhost:19000/events | python3 -c '
import json,sys
for e in json.load(sys.stdin).get("events", []):
    md = e.get("metadata") or {}
    if "review_status" not in md and "ai_status" not in md:
        continue
    print("  %-9s review=%-11s ai=%-8s risk=%-6s %s" % (
        "ALLOW" if e.get("allowed") else "DENY",
        md.get("review_status", "-"), md.get("ai_status", "-"),
        md.get("risk_level", "-"), (e.get("statement") or "")[:52]))
' 2>/dev/null

h "gate counters"
curl -s localhost:19000/stats | python3 -c '
import json,sys
for l in json.load(sys.stdin)["listeners"]:
    r = l.get("review")
    if r:
        print("  %-8s claims=%d approvals=%d filed=%d refusals=%d errors=%d" % (
            l["name"], r["claims"], r["approvals"], r["filed"], r["refusals"], r["errors"]))
' 2>/dev/null

echo
good "done"
note "Reviews:  curl -s localhost:8009/api/reviews | python3 -m json.tool"
note "Sessions: curl -s localhost:19000/api/sessions | python3 -m json.tool"
note "Logs:     docker compose logs -f hoop-inspect hoop-gateway"
echo
