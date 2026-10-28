#!/usr/bin/env bash
#
# Verifies the credential-swap overlay: an ID token logs in and the database
# sees the pool's role, a forged token is refused at the door, the shared
# role cannot be escaped, the GKE lane impersonates the caller, and the audit
# trail names the principal and the credential.
#
#   cd deploy/docker-compose/envoy-stack && ./run.sh --rebuild
#   docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml \
#     -f credentials/docker-compose.credentials.yml up -d --build --wait
#   ./credentials-check.sh
#
# The counterpart to pg-kerberos-check.sh for a lane where the sidecar, not
# the database, authenticates the person.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STACK="$(cd "$HERE/../../../deploy/docker-compose/envoy-stack" && pwd)"
cd "$STACK"

COMPOSE=(-f docker-compose.yml -f gke/docker-compose.gke.yml -f credentials/docker-compose.credentials.yml)
dc() { docker compose "${COMPOSE[@]}" "$@"; }

PASS=0; FAIL=0
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
ok()   { printf '\033[32m  PASS\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '\033[31m  FAIL\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
note() { printf '\033[90m       %s\033[0m\n' "$*"; }

GW=https://connectgateway.googleapis.com/v1/projects/123456789012/locations/global/gkeMemberships/demo

# The sidecar terminates TLS and answers the login itself, so
# SCRAM-SHA-256-PLUS is not offered: channel_binding=disable.
PGOPTS="host=envoy port=5432 dbname=appdb sslmode=require channel_binding=disable"
pg() { # pg <user> <token> <psql args>
    local u="$1" k="$2"; shift 2
    dc exec -T -e PGPASSWORD="$k" client psql "$PGOPTS user=$u" "$@" 2>&1
}
kc()   { dc exec -T client kubectl "$@" 2>&1; }
curlc() { dc exec -T client curl -sS --cacert /etc/gke-pki/ca.crt "$@" 2>&1; }

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not up. From the envoy-stack directory:" >&2
    echo "  docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml -f credentials/docker-compose.credentials.yml up -d --build --wait" >&2
    exit 1
fi

# The client installs its packages at container start; the first psql after
# a fresh `up` can land before they are there.
for _ in $(seq 1 20); do
    dc exec -T client sh -c 'command -v psql && command -v kubectl' >/dev/null 2>&1 && break
    sleep 2
done
# The ID tokens fake-google issued at startup: what the person holds after
# SSO. kubectl reads the same files through the kubeconfig.
ALICE_PG="$(dc exec -T client cat /etc/gke-tokens/alice 2>/dev/null)"
BOB_PG="$(dc exec -T client cat /etc/gke-tokens/bob 2>/dev/null)"
if [[ -z "$ALICE_PG" || -z "$BOB_PG" ]]; then
    echo "no ID tokens under /etc/gke-tokens in the client; is the credentials overlay up?" >&2
    exit 1
fi

CHECK_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1

# ----------------------------------------------------------- 1. the swap
c_step "1. The ID token logs in, and the database sees the pool's role"
WHO="$(pg alice@example.com "$ALICE_PG" -tAc "SELECT current_user || ' ' || application_name FROM pg_stat_activity WHERE pid = pg_backend_pid()")"
if grep -q '^appuser alice@example.com via demo$' <<<"$WHO"; then
    ok "alice's token selected rw: upstream user appuser, application_name names her"
else
    bad "expected 'appuser alice@example.com via demo', got: $WHO"
fi

WHO="$(pg bob@example.com "$BOB_PG" -tAc "SELECT current_user")"
if grep -q '^app_ro$' <<<"$WHO"; then
    ok "bob's token selected ro: upstream user app_ro"
else
    bad "expected app_ro for bob, got: $WHO"
fi

INS="$(pg bob@example.com "$BOB_PG" -tAc "INSERT INTO customers (name,email,ssn,cpf,iban) VALUES ('x','x@x','1','1','1')")"
if grep -qi 'permission denied' <<<"$INS"; then
    ok "the read-only role is enforced by the database"
else
    bad "expected permission denied for app_ro, got: $INS"
fi

# ---------------------------------------------------------- 2. refusals
c_step "2. A forged token is refused at the door"
BAD="$(pg alice@example.com "forged.token.value" -tAc "SELECT 1")"
if grep -qiE 'password authentication failed|login refused|authentication failed' <<<"$BAD" && ! grep -qE '^ *1$' <<<"$BAD"; then
    ok "forged token refused: $(head -1 <<<"$BAD")"
else
    bad "forged token was not refused: $BAD"
fi

ROLE="$(pg alice@example.com "$ALICE_PG" -tAc "SET ROLE app_ro")"
if grep -q 'SET' <<<"$ROLE" && ! grep -qiE 'not permitted|refused|denied' <<<"$ROLE"; then
    bad "SET ROLE went through on a pool lane: $ROLE"
else
    ok "SET ROLE refused inside the swapped session"
fi

DEL="$(pg alice@example.com "$ALICE_PG" -tAc 'DELETE FROM customers WHERE id = 1')"
if grep -q 'destructive statements are not permitted on appdb' <<<"$DEL"; then
    ok "the lane's own guardrail still applies to a swapped session"
else
    bad "guardrail did not fire: $DEL"
fi

# -------------------------------------------------------- 3. the GKE lane
c_step "3. The GKE lane impersonates the caller behind a service account"
PODS="$(kc get pods)"
if grep -q '^demo ' <<<"$PODS"; then
    ok "alice's token reaches the cluster as alice@example.com (admin)"
else
    bad "kubectl as alice failed: $PODS"
fi

BOBPODS="$(kc --context bob get pods)"
if grep -qi 'forbidden' <<<"$BOBPODS" && grep -q 'bob@example.com' <<<"$BOBPODS"; then
    ok "bob's token reaches the cluster as bob@example.com, refused by RBAC"
else
    bad "expected Forbidden naming bob@example.com, got: $BOBPODS"
fi

CODE="$(curlc -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer forged.token.value' "$GW/api/v1/namespaces/default/pods")"
if [[ "$CODE" == "401" || "$CODE" == "403" ]]; then
    ok "a forged token is refused by the sidecar ($CODE)"
else
    bad "forged token answered $CODE"
fi

sleep 1
FG="$(dc logs fake-google --no-log-prefix --since "$CHECK_START" 2>&1)"
if grep -q 'generateAccessToken sa=hoop-sidecar@demo-project.iam.gserviceaccount.com: ok' <<<"$FG"; then
    ok "the sidecar minted the service account token through impersonation"
else
    bad "no generateAccessToken in fake-google's log"
fi
if grep -q 'gateway GET .* as hoop-sidecar@demo-project.iam.gserviceaccount.com' <<<"$FG"; then
    ok "Connect Gateway saw the service account, not a person's token"
else
    bad "gateway did not act as the service account"
fi

# ------------------------------------------------------- 4. the audit trail
c_step "4. The audit trail names the principal and the credential"
LOG="$(dc logs hoop-inspect --no-log-prefix --since "$CHECK_START" 2>&1)"
START="$(grep '"kind":"session_start"' <<<"$LOG" | grep '"connection":"appdb"' | grep 'alice@example.com' | tail -1)"
if [[ -n "$START" ]] && grep -q '"credential":"rw"' <<<"$START" && grep -q '"upstream_user":"appuser"' <<<"$START" \
   && grep -q '"identity_type":"jwt"' <<<"$START" && grep -q '"verified":"true"' <<<"$START"; then
    ok "session_start: principal alice@example.com, credential rw, upstream_user appuser, verified"
else
    bad "session_start metadata incomplete: ${START:-<none>}"
fi

GKESTART="$(grep '"kind":"session_start"' <<<"$LOG" | grep '"connection":"gke"' | grep 'alice@example.com' | tail -1)"
if [[ -n "$GKESTART" ]] && grep -q '"credential":"gateway"' <<<"$GKESTART"; then
    ok "gke session_start: principal alice@example.com, credential gateway"
else
    bad "gke session_start missing or without credential: ${GKESTART:-<none>}"
fi

# The relay records a refused login as a violation under the rule "identity"
# (the claimed user as principal); older wording was login_refused.
if grep '"kind":"violation"' <<<"$LOG" | grep 'alice@example.com' | grep -qE '"rule":"(identity|login_refused)"'; then
    ok "the refused login was recorded as a violation under the claimed user"
else
    bad "no identity violation for the refused login in the trail"
fi

SECRETS="$(grep -cF -e "$ALICE_PG" -e "$BOB_PG" -e apppass -e ro-secret -e tok-sa-gateway <<<"$LOG")"
if [[ "$SECRETS" == "0" ]]; then
    ok "no token or password in the trail"
else
    bad "$SECRETS line(s) of the trail hold a secret"
fi

# ------------------------------------------------------------------ verdict
printf '\n\033[1m%s\033[0m\n' "─────────────────────────────────────────────"
if (( FAIL == 0 )); then
    printf '\033[32m%d passed\033[0m\n' "$PASS"
else
    printf '\033[32m%d passed\033[0m, \033[31m%d failed\033[0m\n' "$PASS" "$FAIL"
fi
exit $(( FAIL > 0 ))
