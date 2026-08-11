#!/usr/bin/env bash
#
# Verifies the Postgres lane: TLS, Kerberos, inspection, masking, the audit
# trail and the GSS-encryption refusal.
#
#   ./pg-stack.sh && ./pg-kerberos-check.sh
#
# The counterpart to mssql-kerberos-check.sh, and the lane where a Kerberos
# login succeeds rather than stopping at the database.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STACK="$(cd "$HERE/../../../deploy/docker-compose/envoy-stack" && pwd)"
cd "$STACK"

COMPOSE=(-f docker-compose.yml -f postgres/docker-compose.postgres.yml)
dc() { docker compose "${COMPOSE[@]}" "$@"; }

PASS=0; FAIL=0
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
ok()   { printf '\033[32m  PASS\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '\033[31m  FAIL\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
note() { printf '\033[90m       %s\033[0m\n' "$*"; }

PGOPTS="sslmode=require channel_binding=disable gssencmode=disable"

pw()  { dc exec -T client env PGPASSWORD=apppass \
          psql "host=envoy port=5432 dbname=appdb user=appuser $PGOPTS" "$@" 2>&1; }
krb() { dc exec -T client \
          psql "host=appdb.hoop.test port=5432 dbname=appdb user=alice@HOOP.TEST $PGOPTS" "$@" 2>&1; }

# Retry rather than assume. The client installs its packages at container
# start, so the first kinit after a fresh `up` can land before krb5 is there,
# and every later check then fails for a reason that has nothing to do with
# what it tests.
for _ in $(seq 1 20); do
    dc exec -T client sh -c 'echo alicepass | kinit alice@HOOP.TEST' >/dev/null 2>&1
    if dc exec -T client klist 2>/dev/null | grep -q 'krbtgt/HOOP.TEST'; then
        break
    fi
    sleep 2
done

# --------------------------------------------------------------- 1. TLS
c_step "1. Envoy terminates the lane's TLS"
if grep -qE '^ *1$' <<<"$(pw -tAc 'SELECT 1')"; then
    ok "psql connects with sslmode=require"
else
    bad "TLS connection refused"
    note "$(pw -tAc 'SELECT 1' | head -2)"
fi

STATS="$(curl -s http://localhost:9901/stats 2>/dev/null)"
TLS_TERMINATED="$(grep -oE 'postgres\.ingress_pg\.sessions_terminated_ssl: [0-9]+' <<<"$STATS" | grep -oE '[0-9]+$')"
if [[ "${TLS_TERMINATED:-0}" -gt 0 ]]; then
    ok "envoy reports sessions_terminated_ssl=$TLS_TERMINATED"
else
    bad "envoy terminated no SSL sessions; something else answered the SSLRequest"
fi

# ---------------------------------------------------------- 2. Kerberos
c_step "2. Kerberos authenticates, and the transport stays readable"
KL="$(dc exec -T client klist 2>&1)"
if grep -q 'krbtgt/HOOP.TEST' <<<"$KL"; then
    ok "the OS holds a TGT for alice@HOOP.TEST"
else
    bad "kinit produced no TGT"
fi

# Casting a bool through || renders it "true"/"false", while column output
# renders "t"/"f". Ask for the text form and match that.
GSS="$(krb -tAc "SELECT gss_authenticated||' '||encrypted FROM pg_stat_gssapi WHERE pid=pg_backend_pid()")"
if grep -q '^true false$' <<<"$GSS"; then
    ok "postgres reports gss_authenticated=true, encrypted=false"
    note "authenticated by ticket, transport left readable — the whole design"
else
    bad "unexpected GSSAPI state: $(head -1 <<<"$GSS")"
fi

if grep -q 'postgres/appdb.hoop.test' <<<"$(dc exec -T client klist 2>&1)"; then
    ok "the KDC issued a service ticket for postgres/appdb.hoop.test"
    note "that name resolves to Envoy; the ticket is for the database's key"
else
    bad "no service ticket was issued for the SPN"
fi

WHO="$(krb -tAc "SELECT split_part(current_user,'@',1)")"
if grep -q 'alice' <<<"$WHO"; then
    ok "session runs as the Kerberos principal"
else
    bad "unexpected current_user: $(head -1 <<<"$WHO")"
fi

# ------------------------------------------------- 3. GSS-encryption refusal
c_step "3. GSS encryption is refused, so the relay keeps seeing statements"
# libpq's DEFAULT is gssencmode=prefer. Accepted, the session would be
# ciphertext and the gate would report nothing at all. This is the case that
# regressed silently before the relay learned to answer 'N'.
DEF="$(dc exec -T client \
        psql "host=appdb.hoop.test port=5432 dbname=appdb user=alice@HOOP.TEST sslmode=disable" \
        -tAc "SELECT 'default gssencmode reached the gate'" 2>&1)"
if grep -q 'reached the gate' <<<"$DEF"; then
    ok "a DEFAULT-configured Kerberos client connects"
else
    bad "the default client configuration failed"
    note "$(head -2 <<<"$DEF")"
fi

sleep 1
LOG="$(dc logs hoop-inspect --no-log-prefix --since 2m 2>&1)"
if grep -q 'default gssencmode reached the gate' <<<"$LOG"; then
    ok "and its statement is IN THE AUDIT TRAIL, so the session stayed readable"
else
    bad "the statement never reached the audit trail; the relay went blind"
fi

# ---------------------------------------------------- 4. policy and masking
c_step "4. Policy and masking, on a Kerberos session"
DEL="$(krb -tAc 'DELETE FROM customers WHERE id = 1')"
if grep -q 'destructive statements are not permitted on appdb' <<<"$DEL"; then
    ok "DELETE denied, operator message reached psql"
else
    bad "DELETE was not denied"
    note "$(head -2 <<<"$DEL")"
fi

if grep -qE '^ *3$' <<<"$(pw -tAc 'SELECT count(*) FROM customers')"; then
    ok "rows intact, so the DELETE never reached the database"
else
    bad "row count changed; the denial did not stop the statement"
fi

MASKED="$(krb -tAc 'SELECT email, ssn FROM customers ORDER BY id')"
if grep -q 'REDACTED:EMAIL_ADDRESS' <<<"$MASKED"; then
    ok "email redacted by the entity rule"
else
    bad "email was not redacted"
    note "$(head -2 <<<"$MASKED")"
fi
if grep -q '\*\*\*-\*\*-6789' <<<"$MASKED"; then
    ok "ssn masked by the column rule, which detection alone would miss"
else
    bad "ssn was not masked"
fi
if grep -qE 'ada@example\.com|grace@example\.com' <<<"$MASKED"; then
    bad "a raw address reached the client despite masking being reported"
else
    ok "no raw address reached the client"
fi

# ------------------------------------------------------- 5. the audit trail
c_step "5. The audit trail names the actor"
sleep 1
SESS="$(curl -s 'http://localhost:19000/api/sessions?limit=25' 2>/dev/null)"
if grep -q 'alice@HOOP.TEST' <<<"$SESS"; then
    ok "sessions carry principal=alice@HOOP.TEST"
    note "pgwire names its user in cleartext; MSSQL hides it inside the ticket"
else
    bad "no session recorded the Kerberos principal"
fi

# Re-read the log. $LOG was captured before section 4 ran the DELETE, so
# matching against it passes only when a PREVIOUS run left a violation inside
# the window: green on a rerun, red on a fresh stack, and wrong both times.
LOG_NOW="$(dc logs hoop-inspect --no-log-prefix --since 2m 2>&1)"
VIOL="$(grep '"kind":"violation"' <<<"$LOG_NOW" | tail -1)"
if grep -q 'no-destructive-sql' <<<"$VIOL" && grep -q 'alice@HOOP.TEST' <<<"$VIOL"; then
    ok "the denial names both the rule and the principal"
else
    bad "the violation event is missing the rule or the principal"
    note "$(cut -c1-90 <<<"$VIOL")"
fi

# ------------------------------------------------------------------ verdict
printf '\n\033[1m%s\033[0m\n' "─────────────────────────────────────────────"
if (( FAIL == 0 )); then
    printf '\033[32m%d passed\033[0m, 0 failed\n' "$PASS"
else
    printf '\033[32m%d passed\033[0m, \033[31m%d failed\033[0m\n' "$PASS" "$FAIL"
fi
exit $(( FAIL > 0 ))
