#!/usr/bin/env bash
#
# Verifies the SQL Server 2019 lane end to end.
#
#   ./mssql2019-check.sh
#
# Bring the stack up first with ./mssql2019-stack.sh.
#
# The point of this lane is one wire fact: PRELOGIN's ENCRYPT_OFF means
# "encrypt the login only", not "no TLS". SQL Server 2019 with no certificate
# configured still puts the LOGIN7 packet inside TLS, using a self-signed
# certificate it mints at startup. The relay therefore meets a raw TLS record
# where a TDS packet header should be. Section 2 proves it recovers; section 5
# proves the case it cannot recover fails closed instead of running blind.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE/../../../deploy/docker-compose/envoy-stack/mssql2019" || exit 1

c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
ok()   { printf '\033[32m  PASS\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '\033[31m  FAIL\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
note() { printf '\033[90m       %s\033[0m\n' "$*"; }

PASS=0; FAIL=0

dc() { docker compose -f docker-compose.mssql2019.yml "$@"; }

# Through the relay, Encrypt=Optional. The driver sends ENCRYPT_OFF, so the
# server encrypts the login packet and leaves every statement in the clear.
relay_sql() {
    dc exec -T sqlclient sqlcmd -S hoop-inspect,11433 \
        -U appuser -P 'App!Passw0rd' -No -C -d appdb -b "$@" 2>&1
}

# Straight at the database, bypassing the relay. The control for every claim
# below: a failure here belongs to SQL Server, not to hoop-inspect.
direct_sql() {
    dc exec -T sqlclient sqlcmd -S mssql2019,1433 \
        -U appuser -P 'App!Passw0rd' -No -C -d appdb -b "$@" 2>&1
}

# ------------------------------------------------------------ 1. unit tests
c_step "1. Codec unit tests"
CODEC_OUT="$(cd "$HERE/../.." && go test ./codec/mssql/ -count=1 -v 2>&1)"
if grep -q '^ok\|^PASS' <<<"$CODEC_OUT"; then
    ok "codec/mssql tests green"
    note "$(grep -c '^    --- PASS\|^--- PASS' <<<"$CODEC_OUT") test cases"
else
    bad "codec/mssql tests failed"
fi

# --------------------------------------------- 2. the encrypted login itself
c_step "2. The lane carries a query through an ENCRYPTED login (the fix)"
OUT="$(relay_sql -Q "SELECT name FROM customers ORDER BY id")"
if grep -q "Ada Lovelace" <<<"$OUT"; then
    ok "SELECT returned rows through the relay"
else
    bad "SELECT failed through the relay"
    note "$(head -5 <<<"$OUT")"
fi

# The claim under test is not "a query worked" but "the login was ciphertext
# and the relay resynchronized anyway". The metadata key is the relay's own
# report that it passed an unreadable login through.
#
# Read it from the JSONL trail, not /api/sessions: that endpoint serves
# per-session SUMMARIES (counts and a verdict) and carries no statement
# fields. Capture first, then match. `dc logs ... | grep -q` looks equivalent
# and is not: grep -q exits on the first hit, docker takes SIGPIPE, and the
# pipeline reports a failure that never happened.
c_step "2b. The relay recorded that the login was encrypted"
TRAIL="$(dc logs hoop-inspect 2>&1)"
if grep -q 'mssql.login_encrypted' <<<"$TRAIL"; then
    ok "statements carry mssql.login_encrypted"
    note "the login crossed as TLS records; the statements after it did not"
    note "$(grep -m1 -o '"statement":"[^"]*"[^}]*"mssql.login_encrypted":"true"' <<<"$TRAIL" | head -c 160)"
else
    bad "no statement carries mssql.login_encrypted"
    note "if statements ARE present without it, the login was in the clear"
    note "and this stack is not reproducing the case it exists for"
fi

# ------------------------------------------------------------ 3. guardrail
c_step "3. The guardrail refuses a DELETE, in TDS's own error frame"
OUT="$(relay_sql -Q "DELETE FROM customers WHERE id = 1")"
if grep -q "destructive statements are not permitted on mssql2019" <<<"$OUT"; then
    ok "DELETE denied, operator message reached sqlcmd"
    note "$(grep -m1 'destructive' <<<"$OUT" | sed 's/^[[:space:]]*//')"
else
    bad "DELETE was not denied with the operator's message"
    note "$(head -5 <<<"$OUT")"
fi

# appuser holds db_owner, so the database would have run it. Proving the row
# survived is what separates a guardrail from a permissions check.
ROWS="$(direct_sql -h-1 -W -Q "SET NOCOUNT ON; SELECT COUNT(*) FROM customers")"
if grep -qE '(^|[^0-9])2([^0-9]|$)' <<<"$ROWS"; then
    ok "both rows still present; the DELETE stopped at the relay"
else
    bad "row count is not 2 after the refused DELETE"
    note "$(head -3 <<<"$ROWS")"
fi

# -------------------------------------------------------------- 4. masking
c_step "4. Response masking rewrites the TDS row frames"
MASKED="$(relay_sql -h-1 -W -Q "SET NOCOUNT ON; SELECT email, ssn, notes FROM customers")"
if grep -q 'ada@example.com' <<<"$MASKED"; then
    bad "the email came back in the clear"
else
    ok "email redacted"
fi
if grep -q '123-45-6789' <<<"$MASKED"; then
    bad "the SSN came back in the clear"
else
    ok "SSN masked by the column rule"
fi
# NVARCHAR(MAX) uses PLP framing, a different encoding from NVARCHAR(200).
# A rewriter that handles only the USHORT form leaks exactly this column.
if grep -q 'ada.personal@example.com' <<<"$MASKED"; then
    bad "the NVARCHAR(MAX) notes column leaked an address"
else
    ok "NVARCHAR(MAX) masked through its PLP encoding"
fi

# ------------------------------------------------- 5. the fail-closed case
c_step "5. Encrypt=Mandatory encrypts everything, so the relay refuses"
# -Nm makes the client require encryption, the server answers ENCRYPT_ON, and
# the whole session is TLS. No statement on it can be classified, masked or
# audited. Forwarding it would be a silent bypass, so the codec denies.
OUT="$(dc exec -T sqlclient sqlcmd -S hoop-inspect,11433 \
        -U appuser -P 'App!Passw0rd' -Nm -C -d appdb -b \
        -Q "SELECT name FROM customers" 2>&1)"
if grep -q "Ada Lovelace" <<<"$OUT"; then
    bad "a fully encrypted session ran uninspected"
    note "this is the silent bypass the fail-closed path exists to prevent"
else
    ok "the fully encrypted session did not return data"
    note "$(head -3 <<<"$OUT" | tr -d '\r' | sed 's/^[[:space:]]*//' | paste -sd' ' -)"
fi

# "No rows came back" is necessary and nowhere near sufficient: a client that
# stalls until its own login timeout also returns no rows, and an earlier
# revision of this lane did exactly that for eight seconds while the relay
# logged nothing. Demand the refusal itself.
if grep -q 'stream-unsafe' <<<"$(dc logs hoop-inspect 2>&1)"; then
    ok "the relay refused it, naming the rule"
    note "denied in milliseconds, not stalled until the driver gave up"
else
    bad "no stream-unsafe denial recorded"
    note "the session went nowhere without the relay saying why, which leaves"
    note "an operator reading a login timeout and no cause"
fi

# The same client parameters straight at the database MUST work. Without this
# control, "Mandatory fails" could just mean the server refused it.
OUT="$(dc exec -T sqlclient sqlcmd -S mssql2019,1433 \
        -U appuser -P 'App!Passw0rd' -Nm -C -d appdb -b \
        -Q "SELECT name FROM customers" 2>&1)"
if grep -q "Ada Lovelace" <<<"$OUT"; then
    ok "the same Mandatory login succeeds straight at SQL Server"
    note "so the refusal above belongs to the relay, by design, not to 2019"
else
    bad "Mandatory fails at SQL Server too; section 5 proves nothing"
    note "$(head -3 <<<"$OUT")"
fi

# --------------------------------------------------------------- 6. audit
c_step "6. The audit trail recorded the statement and the verdict"
AUDIT="$(curl -s 'http://localhost:19001/api/sessions?limit=25' 2>/dev/null)"
# Re-read: the copy taken in section 2b predates the refused DELETE.
TRAIL="$(dc logs hoop-inspect 2>&1)"
if grep -q 'mssql2019' <<<"$AUDIT"; then
    ok "mssql2019 sessions present in the query API"
else
    bad "no mssql2019 session recorded"
fi
if grep -q '"denied_count":[1-9]' <<<"$AUDIT"; then
    ok "a session reports a denial"
else
    bad "no session reports denied_count > 0"
fi
# The rule name lives on the violation record, not in the session summary.
if grep -q '"kind":"violation".*no-destructive-tsql' <<<"$TRAIL"; then
    ok "the violation record names the rule and the statement"
    note "$(grep -m1 -o '"kind":"violation"[^}]*"rule":"[^"]*"' <<<"$TRAIL" | head -c 150)"
else
    bad "no violation record naming no-destructive-tsql"
fi

# --------------------------------------------------------------- summary
c_step "Summary"
printf '  \033[32m%d passed\033[0m, \033[31m%d failed\033[0m\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]] || exit 1
