#!/usr/bin/env bash
#
# Verifies the MSSQL lane: the data path, the guardrail, the audit trail, and
# what Kerberos does when it crosses the relay.
#
#   ./mssql-kerberos-check.sh
#
# Bring the stack up first with ./mssql-stack.sh.
#
# The Kerberos section runs the same login down TWO paths — through the relay
# and directly at SQL Server — and compares them. That comparison is the whole
# point: a Kerberos result only means something about hoop-inspect if you know
# what the same login does without it.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STACK="$(cd "$HERE/../../../deploy/docker-compose/envoy-stack" && pwd)"
cd "$STACK"

COMPOSE=(-f docker-compose.yml -f mssql/docker-compose.mssql.yml)
dc() { docker compose "${COMPOSE[@]}" "$@"; }

PASS=0; FAIL=0
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
ok()   { printf '\033[32m  PASS\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '\033[31m  FAIL\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
note() { printf '\033[90m       %s\033[0m\n' "$*"; }
gap()  { printf '\033[33m  GAP \033[0m %s\n' "$*"; }

# Through the relay: envoy(mssql.hoop.test:1433) -> hoop-inspect -> mssql.
# -Ns is TDS 8.0, which is what makes Envoy able to terminate the TLS.
relay_sql() {
    dc exec -T sqlclient sqlcmd -S mssql.hoop.test,1433 \
        -U appuser -P 'App!Passw0rd' -Ns -d appdb -b "$@" 2>&1
}

# ---------------------------------------------------------------- 1. codec
c_step "1. Codec unit tests"
CODEC_OUT="$(cd "$HERE/../.." && go test ./codec/mssql/ -count=1 -v 2>&1)"
if grep -q '^ok\|^PASS' <<<"$CODEC_OUT"; then
    ok "codec/mssql tests green"
    note "$(grep -c '^--- PASS' <<<"$CODEC_OUT") test cases"
else
    bad "codec/mssql tests failed"
fi

# ----------------------------------------------------------- 2. the data path
c_step "2. The lane carries a query (TDS 8.0 -> Envoy -> relay -> SQL Server)"
OUT="$(relay_sql -Q "SELECT name FROM customers ORDER BY id")"
if grep -q "Ada Lovelace" <<<"$OUT"; then
    ok "SELECT returned rows through the relay"
else
    bad "SELECT did not return the seeded rows"
    note "$(head -3 <<<"$OUT")"
fi

# ------------------------------------------------------------ 3. the guardrail
c_step "3. The guardrail refuses a DELETE, in TDS's own error frame"
OUT="$(relay_sql -Q "DELETE FROM customers WHERE id = 1")"
if grep -q "destructive statements are not permitted on mssqldb" <<<"$OUT"; then
    ok "DELETE denied, operator message reached sqlcmd"
    note "$(grep -m1 'destructive' <<<"$OUT" | sed 's/^[[:space:]]*//')"
else
    bad "DELETE was not denied with the operator's message"
    note "$(head -3 <<<"$OUT")"
fi

# The database would have allowed it; only the relay stopped it. Without this
# check the one above passes just as well against a read-only account, which
# would prove nothing about the guardrail.
OUT="$(relay_sql -Q "SELECT COUNT(*) AS n FROM customers")"
if grep -qE '^ *2$' <<<"$OUT"; then
    ok "both rows still present, so the DELETE never reached the database"
else
    bad "row count changed; the denial did not stop the statement"
    note "$(head -5 <<<"$OUT")"
fi

# ------------------------------------------------------------- 3b. masking
c_step "3b. Response masking rewrites the TDS row frames"
MASKED="$(relay_sql -h-1 -W -Q "SET NOCOUNT ON; SELECT email, ssn, notes FROM customers")"

# The entity detector catches the address.
if grep -q "REDACTED:EMAIL_ADDRESS" <<<"$MASKED"; then
    ok "email redacted by the entity rule"
else
    bad "email was not redacted"
    note "$(head -3 <<<"$MASKED")"
fi

# The column rule catches what the detector refuses: alcatraz rejects
# 123-45-6789 as an obvious fixture, so only `columns: [ssn]` masks it.
if grep -q '\*\*\*-\*\*-6789' <<<"$MASKED"; then
    ok "ssn masked by the column rule, which detection alone would miss"
else
    bad "ssn was not masked"
    note "$(head -3 <<<"$MASKED")"
fi

# NVARCHAR(MAX) travels as PLP: an 8-byte total length, chunks, terminator.
# A rewriter that handles only the USHORT form masks the columns above and
# leaks this one, which looks like success until someone reads a long field.
if grep -q "ada.personal@example.com" <<<"$MASKED"; then
    bad "the NVARCHAR(MAX) column leaked an address (PLP path not masking)"
    note "$(grep -m1 'ada.personal' <<<"$MASKED" | cut -c1-90)"
else
    ok "NVARCHAR(MAX) masked too, so the PLP encoding is covered"
fi

# THE REGRESSION GUARD. An unmeasurable token at the tail of a response once
# made the codec discard the whole rewrite and emit the ORIGINAL bytes, while
# still reporting the cells as masked. The audit trail claimed masking the
# client never received. Checking the count alone would have passed.
if grep -qE 'ada@example\.com|grace@example\.com' <<<"$MASKED"; then
    bad "a raw address reached the client despite masking being reported"
    note "this is the failure mode where the audit trail lies:"
    note "$(grep -m1 -E 'ada@|grace@' <<<"$MASKED" | cut -c1-90)"
else
    ok "no raw address reached the client"
fi

# And the trail agrees with what the client got.
sleep 1
MC="$(curl -s 'http://localhost:19000/api/sessions?limit=1' 2>/dev/null \
      | python3 -c "import sys,json;print(json.load(sys.stdin)['sessions'][0]['masked_count'])" 2>/dev/null)"
if [[ "${MC:-0}" -gt 0 ]]; then
    ok "audit recorded masked_count=$MC for that session"
else
    bad "masking happened but the audit trail recorded none"
fi

# ------------------------------------------------- 3c. Envoy terminates TLS
c_step "3c. Envoy terminates TLS on all three lanes"

pg_tls() {
    dc exec -T client env PGPASSWORD=apppass psql \
        "host=envoy port=5432 dbname=appdb user=appuser $1" -tAc "SELECT 1" 2>&1
}

if grep -qE '^ *1$' <<<"$(pg_tls 'sslmode=require channel_binding=disable gssencmode=disable')"; then
    ok "postgres over TLS, terminated by Envoy's postgres_proxy filter"
else
    bad "postgres refused a TLS connection"
    note "$(pg_tls 'sslmode=require channel_binding=disable gssencmode=disable' | head -2)"
fi

# The two parameters are load-bearing, and each fails LOUDLY when dropped.
# Asserting that is what keeps them from looking like cargo cult in the docs.
if grep -qi "channel binding" <<<"$(pg_tls 'sslmode=require gssencmode=disable')"; then
    ok "without channel_binding=disable it fails, and says why"
    note "the relay strips SCRAM-SHA-256-PLUS; a client on TLS reads that as a downgrade"
else
    note "channel_binding=disable no longer required — check whether the relay still strips PLUS"
fi

# Envoy's own counters are the proof that IT terminated, not something else.
STATS="$(curl -s http://localhost:9901/stats 2>/dev/null)"
TLS_TERMINATED="$(grep -oE 'postgres\.ingress_pg\.sessions_terminated_ssl: [0-9]+' <<<"$STATS" | grep -oE '[0-9]+$')"
if [[ "${TLS_TERMINATED:-0}" -gt 0 ]]; then
    ok "envoy reports sessions_terminated_ssl=$TLS_TERMINATED"
else
    bad "envoy terminated no SSL sessions; something else answered the SSLRequest"
fi

# ----------------------------------------------------------- 4. audit trail
c_step "4. The audit trail recorded the statement and the verdict"
AUDIT="$(curl -s 'http://localhost:19000/api/sessions?limit=25' 2>/dev/null)"
if grep -q 'mssqldb' <<<"$AUDIT"; then
    ok "mssqldb sessions present in the sidecar's query API"
else
    bad "no mssqldb session recorded"
fi
# Capture first, then match. `dc logs ... | grep -q` looks equivalent and is
# not: grep -q exits on the first hit, docker takes SIGPIPE, and `set -o
# pipefail` reports the whole pipeline as failed. It passes while the log is
# short and starts lying once it grows.
RELAY_LOG="$(dc logs hoop-inspect --no-log-prefix --since 5m 2>&1)"
if grep -q '"rule":"no-destructive-tsql"' <<<"$RELAY_LOG"; then
    ok "the denial is in the audit stream, keyed to the rule that fired"
else
    bad "no audit event naming no-destructive-tsql"
fi

# ------------------------------------------------------------- 5. Kerberos
c_step "5. Kerberos"

dc exec -T sqlclient bash -c 'kdestroy 2>/dev/null; echo alicepass | kinit alice@HOOP.TEST' >/dev/null 2>&1
KLIST="$(dc exec -T sqlclient klist 2>&1)"
if grep -q 'krbtgt/HOOP.TEST' <<<"$KLIST"; then
    ok "the OS holds a TGT for alice@HOOP.TEST"
else
    bad "kinit did not produce a TGT"
fi

# Dialling mssql.hoop.test makes the driver ask for MSSQLSvc/mssql.hoop.test:1433.
# That name resolves to ENVOY, and the ticket is still issued and still only
# decryptable by SQL Server — a service ticket names a service, not a host.
KRB_RELAY="$(dc exec -T sqlclient bash -c \
    'sqlcmd -S mssql.hoop.test,1433 -E -Ns -d appdb -Q "SELECT SUSER_SNAME()" 2>&1 | head -2')"
KLIST="$(dc exec -T sqlclient klist 2>&1)"

if grep -q 'MSSQLSvc/mssql.hoop.test:1433' <<<"$KLIST"; then
    ok "the KDC issued a service ticket for MSSQLSvc/mssql.hoop.test:1433"
    note "that name resolves to Envoy; the ticket is for SQL Server's key"
else
    bad "no service ticket was issued for the SPN"
fi

# The relay must have carried the login far enough for SQL Server to form an
# opinion about it. A session on the mssqldb lane with zero statements is
# exactly that: login bytes crossed, no SQL followed.
KRB_LOG="$(dc logs hoop-inspect --no-log-prefix --since 2m 2>&1)"
if grep '"msg":"session opened"' <<<"$KRB_LOG" | grep -q '"listener":"mssqldb"'; then
    ok "hoop-inspect opened an mssqldb session for the Kerberos attempt"
    note "LOGIN7 (0x10) and SSPI (0x11) packets forwarded verbatim"
else
    bad "the Kerberos attempt did not reach the relay"
fi

# ---- the control -----------------------------------------------------------
# The same login, straight at SQL Server. sqlhost.hoop.test bypasses Envoy and
# hoop-inspect entirely.
dc exec -T sqlclient bash -c 'kdestroy 2>/dev/null; echo alicepass | kinit alice@HOOP.TEST' >/dev/null 2>&1
KRB_DIRECT="$(dc exec -T sqlclient bash -c \
    'sqlcmd -S sqlhost.hoop.test,1433 -E -Nm -C -d appdb -Q "SELECT SUSER_SNAME()" 2>&1 | head -2')"

norm() { sed -e 's/[0-9]//g' -e 's/^[[:space:]]*//' <<<"$1" | tr -d '\n'; }

if grep -q 'HOOP' <<<"$KRB_RELAY" && ! grep -qi 'error\|failed' <<<"$KRB_RELAY"; then
    ok "authenticated through the relay as $(tr -d ' \n' <<<"$KRB_RELAY" | tail -c 32)"
elif [[ "$(norm "$KRB_RELAY")" == "$(norm "$KRB_DIRECT")" ]]; then
    gap "the AD login is refused, IDENTICALLY with and without the relay"
    note "through relay : $(head -1 <<<"$KRB_RELAY" | cut -c1-110)"
    note "direct to sql : $(head -1 <<<"$KRB_DIRECT" | cut -c1-110)"
    note ""
    note "Same result on both paths, so hoop-inspect is not the cause: it"
    note "carried the ticket to a server that would have refused it anyway."
    note "SQL Server on Linux is not resolving HOOP\\alice against the"
    note "directory (error 18452). See mssql/README.md."
    PASS=$((PASS+1))   # the relay behaved correctly; the gap is downstream
else
    bad "the relay path and the direct path disagree — that WOULD be our bug"
    note "through relay : $(head -1 <<<"$KRB_RELAY" | cut -c1-110)"
    note "direct to sql : $(head -1 <<<"$KRB_DIRECT" | cut -c1-110)"
fi

# --------------------------------------------------- 6. the bypass guard
c_step "6. The routing-redirect guard"
GUARD_OUT="$(cd "$HERE/../.." && go test ./codec/mssql/ -run Routing -count=1 -v 2>&1)"
if grep -q '^--- PASS: TestRoutingRedirectIsRefused' <<<"$GUARD_OUT" \
   && grep -q '^--- PASS: TestRoutingRedirectSplitAcrossReadsIsStillRefused' <<<"$GUARD_OUT"; then
    ok "a routing ENVCHANGE is refused rather than forwarded"
    note "forwarding it would move the client to a socket the relay does not"
    note "hold: no policy, no audit, and no trace of having stopped watching"
else
    bad "the routing-redirect guard is not holding"
fi

# ------------------------------------------------------------------ verdict
printf '\n\033[1m%s\033[0m\n' "─────────────────────────────────────────────"
if (( FAIL == 0 )); then
    printf '\033[32m%d passed\033[0m, 0 failed\n' "$PASS"
else
    printf '\033[32m%d passed\033[0m, \033[31m%d failed\033[0m\n' "$PASS" "$FAIL"
fi
exit $(( FAIL > 0 ))
