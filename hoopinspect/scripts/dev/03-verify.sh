#!/usr/bin/env bash
#
# Asserts what an operator would actually observe, against a live relay
# talking to real Vertex.
#
# Every check below either passes or prints what it saw instead. The script
# exits non-zero if any fail, so it works in CI as well as by hand.
#
# Usage:
#   ./03-verify.sh
#
# Costs about four Vertex calls. The rest of the statements are served by the
# trigger or the cache, which is the point.

set -uo pipefail
cd "$(dirname "$0")"
source ./lib.sh

curl -sf "http://127.0.0.1:${ADMIN_PORT}/healthz" >/dev/null \
    || die "the relay is not running. Run ./02-run.sh first."
command -v psql >/dev/null || die "psql is not installed"

PASS=0; FAIL=0

# A model can answer with stop_reason "refusal" and no content. Under
# fail_open that is an ALLOW, so an enforcement assertion fails and the cause
# looks like a broken guardrail. It is not: the relay did its job and the
# model declined. Name it, because the fix is a different model rather than a
# different config.
refusals() { python3 - "$RELAY_LOG" <<'PYEOF'
import json, sys
n = 0
for line in open(sys.argv[1]):
    line = line.strip()
    if not line.startswith("{"):
        continue
    try:
        ev = json.loads(line)
    except Exception:
        continue
    if "refused to classify" in (ev.get("error") or ""):
        n += 1
print(n)
PYEOF
}
check() {  # check <name> <condition-exit-code> <detail-on-failure>
    if [[ "$2" -eq 0 ]]; then ok "$1"; PASS=$((PASS+1))
    else bad "$1"; printf '        %s\n' "$3"; FAIL=$((FAIL+1)); fi
}

calls() { admin /stats | pyq 'import json,sys; print(json.load(sys.stdin).get("version",""))' >/dev/null; }

# Provider calls are not on /stats yet, so the audit stream is the meter: one
# classified statement writes one risk_level line.
classified() { python3 - "$RELAY_LOG" <<'PY'
import json, sys
n = 0
for line in open(sys.argv[1]):
    line = line.strip()
    if not line.startswith("{"): continue
    try: ev = json.loads(line)
    except Exception: continue
    if (ev.get("metadata") or {}).get("risk_level"): n += 1
print(n)
PY
}

# ------------------------------------------------------------- postgres lane
h "Postgres lane"

before=$(classified)
out=$($PGC -c 'SELECT name, email FROM customers;' 2>&1)
[[ "$out" == *"Ada Lovelace"* ]]
check "select reaches the database" $? "$out"

after=$(classified)
[[ "$before" == "$after" ]]
check "select costs no model call (the trigger excluded it)" $? "classified went $before -> $after"

out=$($PGC -c 'DROP TABLE customers;' 2>&1)
[[ "$out" == *"schema changes are not permitted"* ]]
check "a local rule denies before any model call" $? "$out"

after2=$(classified)
[[ "$after" == "$after2" ]]
check "the locally-denied statement never reached a model" $? "classified went $after -> $after2"

h "Postgres lane: the analyzer"
note "This one calls Vertex. Expect a second or two."

out=$($PGC -c "UPDATE customers SET name = 'x';" 2>&1)
[[ "$out" == *"ERROR"* || "$out" == *"FATAL"* ]]
check "an unbounded UPDATE is blocked" $? "$out"

# Assert the denial came from the analyzer, not from postgres. Checking only
# that psql printed something passes on "relation does not exist", which is how
# a broken fixture masquerades as a working guardrail.
msg=$(python3 - "$RELAY_LOG" <<'PYEOF'
import json, sys
out = ""
for line in open(sys.argv[1]):
    line = line.strip()
    if not line.startswith("{"):
        continue
    try:
        ev = json.loads(line)
    except Exception:
        continue
    if ev.get("kind") == "violation" and ev.get("rule") == "risky-writes":
        out = ev.get("message", "")
print(out)
PYEOF
)
[[ -n "$msg" ]]
check "the denial came from the analyzer rule, not from postgres" $? \
    "no violation with rule=risky-writes in the trail; psql said: $(head -1 <<<"$out")"
note "  message: ${msg:-(none)}"

# Same SHAPE, different literal. A cache hit still writes an audit line, so
# the count cannot distinguish one; elapsed time can. Measure a known miss
# first to get a baseline for this provider on this network.
ms() { python3 -c "import time;print(int(time.time()*1000))"; }

t0=$(ms)
out=$($PGC -c "DELETE FROM customers WHERE id = 99999;" 2>&1)   # new shape, a real call
miss=$(( $(ms) - t0 ))

t0=$(ms)
out=$($PGC -c "UPDATE customers SET name = 'y';" 2>&1)          # shape seen above
hit=$(( $(ms) - t0 ))

[[ "$out" == *"ERROR"* || "$out" == *"FATAL"* ]]
check "the same shape with a different literal is blocked too" $? "$out"

# A hit skips the provider entirely, so it should be a small fraction of a
# miss. Comparing against the measured miss keeps this meaningful whether the
# provider is Vertex over the internet or a fake on loopback.
[[ "$hit" -lt $(( miss / 2 )) || "$hit" -lt 50 ]]
check "repeat shape served from cache (${hit}ms vs ${miss}ms uncached)" $? \
    "cache hit took ${hit}ms against an uncached ${miss}ms; the cache is not being consulted"

# ----------------------------------------------------------------- http lane
h "HTTP lane"

code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
    "http://127.0.0.1:${HTTP_RELAY_PORT}/anything" \
    -H 'Content-Type: application/json' -d '{"action":"list","limit":10}')
[[ "$code" == "200" ]]
check "a benign payload passes" $? "got HTTP $code"

body=$(curl -sS -X POST "http://127.0.0.1:${HTTP_RELAY_PORT}/anything" \
    -H 'Content-Type: application/json' \
    -d '{"action":"delete_all_customers","scope":"*","confirm":true}' \
    -w '\n%{http_code}')
code=$(tail -1 <<<"$body")
[[ "$code" == "403" ]]
check "a destructive payload is blocked (needs capture_body)" $? "got HTTP $code: $(head -1 <<<"$body")"
note "  message: $(head -1 <<<"$body")"

before=$(classified)
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
    "http://127.0.0.1:${HTTP_RELAY_PORT}/status/200" -d '{"action":"delete everything"}')
after=$(classified)
[[ "$before" == "$after" ]]
check "an untriggered resource costs no model call" $? "classified went $before -> $after"

# ------------------------------------------------------------------- audit
h "Audit and rollup"

n=$(classified)
[[ "$n" -gt 0 ]]
check "statements carry risk_level into the audit trail ($n)" $? "no event had metadata.risk_level"

got=$(python3 - "$RELAY_LOG" <<'PY'
import json, sys
lv = set()
for line in open(sys.argv[1]):
    line = line.strip()
    if not line.startswith("{"): continue
    try: ev = json.loads(line)
    except Exception: continue
    m = ev.get("metadata") or {}
    if m.get("risk_level"): lv.add(m["risk_level"])
print(",".join(sorted(lv)) or "(none)")
PY
)
[[ "$got" == *high* ]]
check "a high verdict was recorded" $? "levels seen: $got"

risk=$(admin /api/sessions | pyq '
import json,sys
d = json.load(sys.stdin)
rows = d["sessions"] if isinstance(d, dict) else d
print(",".join(sorted({r.get("risk_level","") for r in rows if r.get("risk_level")})) or "(none)")')
[[ "$risk" == *high* ]]
check "the session rollup keeps the highest risk" $? "GET /api/sessions risk_level: $risk"

# --------------------------------------------------------------- no leaks
h "The credential does not leak"

cfg=$(admin /config)
! grep -qiE 'BEGIN [A-Z ]*PRIVATE KEY|"api_key"|service_account|ya29\.' <<<"$cfg"
check "/config carries no credential" $? "$(head -c 200 <<<"$cfg")"

! grep -qiE 'BEGIN [A-Z ]*PRIVATE KEY|ya29\.|service_account' "$RELAY_LOG"
check "the audit stream carries no credential" $? "found credential material in $RELAY_LOG"

host=$(pyq "
import json,sys
d = json.loads(sys.stdin.read())
print((d.get('analyzer') or {}).get('endpoint_host', '(unset)'))" <<<"$cfg")
note "  /config reports endpoint_host: $host"

# ------------------------------------------------------------------ result
hr
r=$(refusals)
if [[ "$r" -gt 0 ]]; then
    printf '\n\033[33m%s\033[0m\n' "the model refused to classify $r statement(s)"
    note "stop_reason \"refusal\" carries no verdict, and fail_open allows the"
    note "statement. Any enforcement check above that failed may have failed for"
    note "this reason rather than for a fault in the relay. Confirm with:"
    note "  python3 -c \"import json;[print(json.loads(l).get('error')) for l in open('$RELAY_LOG') if l.startswith('{') and 'refused' in l]\""
    note "A model that refuses is the wrong model for this job. See README.md."
fi
if [[ $FAIL -eq 0 ]]; then
    printf '\n\033[32m%d checks passed\033[0m\n' "$PASS"
    note "audit stream: $RELAY_LOG"
    exit 0
fi
printf '\n\033[31m%d passed, %d failed\033[0m\n' "$PASS" "$FAIL"
note "relay log: $RELAY_LOG"
exit 1
