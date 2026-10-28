#!/usr/bin/env bash
#
# Walks the credential-swap overlay of the envoy-stack.
#
#   psql    ──TLS──> envoy ──TLS──> hoop-inspect ──TLS──> appdb      as appuser / app_ro
#   kubectl ──TLS──> envoy ──h2c──> hoop-inspect ──TLS──> fake-google ──> k3s, impersonating the caller
#
# Prereqs, from the envoy-stack directory:
#   ./run.sh --rebuild
#   docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml \
#     -f credentials/docker-compose.credentials.yml up -d --build --wait
#
# The client holds ID tokens its identity provider (here fake-google as
# accounts.google.com) issued and nothing else: no database password, no
# Google access token.

set -uo pipefail
cd "$(dirname "$0")/.."

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }

COMPOSE="docker compose --progress quiet -f docker-compose.yml -f gke/docker-compose.gke.yml -f credentials/docker-compose.credentials.yml"
KUBECTL="$COMPOSE exec -T client kubectl"
CURL="$COMPOSE exec -T client curl -sS --cacert /etc/gke-pki/ca.crt"
GW=https://connectgateway.googleapis.com/v1/projects/123456789012/locations/global/gkeMemberships/demo

# channel_binding=disable: the sidecar terminates TLS and answers the login
# itself, so SCRAM-SHA-256-PLUS is not on offer.
PGOPTS="host=envoy port=5432 dbname=appdb sslmode=require channel_binding=disable"
pg() { # pg <user> <token> <psql args>
    local u="$1" k="$2"; shift 2
    $COMPOSE exec -T -e PGPASSWORD="$k" client psql "$PGOPTS user=$u" "$@" 2>&1
}

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not up. From the envoy-stack directory:" >&2
    echo "  docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml -f credentials/docker-compose.credentials.yml up -d --build --wait" >&2
    exit 1
fi

# The ID tokens fake-google issued at startup: what the person holds after
# SSO. kubectl reads the same files through the kubeconfig.
ALICE_PG="$($COMPOSE exec -T client cat /etc/gke-tokens/alice)"
BOB_PG="$($COMPOSE exec -T client cat /etc/gke-tokens/bob)"

DEMO_START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1

h "SWAP / alice logs in with her ID token and the database sees appuser"
note "user=alice@example.com, password=\$ALICE_PG. The sidecar verifies the token,"
note "picks the rw entry (group dba) and logs in upstream as appuser. The"
note "database's own view names the person through application_name:"
pg alice@example.com "$ALICE_PG" -tAc \
    "SELECT current_user || '  ' || application_name FROM pg_stat_activity WHERE pid = pg_backend_pid()" | sed 's/^/  /'

h "SELECT / bob gets the read-only role"
note "Same lane, bob's token: group developers selects ro, so the database sees"
note "app_ro and refuses a write itself, after the sidecar's guardrail refused"
note "the destructive one:"
pg bob@example.com "$BOB_PG" -tAc "SELECT current_user" | sed 's/^/  /'
pg bob@example.com "$BOB_PG" -tAc "DELETE FROM customers WHERE id = 1" | sed 's/^/  /'
pg bob@example.com "$BOB_PG" -tAc "INSERT INTO customers (name,email,ssn,cpf,iban) VALUES ('x','x@x','1','1','1')" | sed 's/^/  /'

h "REFUSED / a forged token never reaches the database"
pg alice@example.com "forged.token.value" -tAc "SELECT 1" | sed 's/^/  /'
note ""
note "And the shared role cannot be escaped from inside the session:"
pg alice@example.com "$ALICE_PG" -tAc "SET ROLE app_ro" | sed 's/^/  /'

h "GKE / kubectl's bearer is the ID token; the cluster sees the caller impersonated"
note "The sidecar swaps the token for the gateway service account's token"
note "(metadata server → iamcredentials:generateAccessToken, both fake-google)"
note "and adds Impersonate-User: alice@example.com. RBAC binds her to admin:"
$KUBECTL get pods 2>&1 | sed 's/^/  /'
note ""
note "bob's token is valid, and bob@example.com has no binding:"
$KUBECTL --context bob get pods 2>&1 | sed 's/^/  /'
note ""
note "A token the issuer did not sign is refused by the sidecar before any upstream:"
$CURL -H 'Authorization: Bearer forged.token.value' "$GW/api/v1/namespaces/default/pods" -w '  [%{http_code}]\n' 2>&1 | sed 's/^/  /'

h "AUDIT / what hoop-inspect recorded"
sleep 1
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null \
    | grep -o '{"kind":"\(session_start\|statement\|violation\)".*' \
    | python3 -c '
import json, sys
for line in sys.stdin:
    try: e = json.loads(line)
    except ValueError: continue
    if e.get("connection") not in ("appdb", "gke"): continue
    meta = e.get("metadata") or {}
    extra = ""
    if e["kind"] == "session_start":
        extra = "credential=%s upstream_user=%s identity_type=%s verified=%s" % (
            meta.get("credential", "-"), meta.get("upstream_user", "-"),
            meta.get("identity_type", "-"), meta.get("verified", "-"))
    else:
        extra = (e.get("statement") or "")[:70] + ("  <" + e["rule"] + ">" if e.get("rule") else "")
    print("  %-13s %-6s %s  %-18s %s" % (e["kind"], e["connection"], e["session_id"][:8], e["principal"], extra))
'
note ""
note "Tokens and database passwords in the trail (expect 0):"
$COMPOSE logs hoop-inspect --since "$DEMO_START" 2>/dev/null | grep -cF -e "$ALICE_PG" -e "$BOB_PG" -e apppass -e ro-secret -e tok-sa-gateway | sed 's/^/  /'
note ""
note "What fake-google saw (impersonation minted once, gateway acting as the service account):"
$COMPOSE logs fake-google --since "$DEMO_START" 2>/dev/null | grep -E 'metadata|generateAccessToken|gateway' | sed -E 's/^[^|]*\| //; s/^/  /' | tail -8
