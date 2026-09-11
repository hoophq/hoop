#!/usr/bin/env bash
#
# Walks the spanner lane, through Envoy and without it.
#
#   grpcurl ──TLS──> envoy:8445 ──ext_authz(OPA)──> hoop-inspect ──h2c──> spanner:9010
#   grpcurl ──h2c──────────────────────────────────> hoop-inspect ──h2c──> spanner:9010
#
# Prereqs, from the envoy-stack directory:
#   ./run.sh
#   docker compose -f docker-compose.yml -f spanner/docker-compose.spanner.yml up -d --wait
#
# The client is the grpcurl service in the overlay, run on demand; nothing
# gRPC needs to be installed on the host. The Cloud Spanner emulator serves
# no reflection, so every call names the descriptor set the
# spanner-descriptors init service built with buf, the same file the
# sidecar reads: -protoset /descriptors/spanner.pb.
#
# Beats that assert do so with ok/fail: the script exits non-zero at the
# first failed check.

set -uo pipefail
cd "$(dirname "$0")/.."

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m  %s\n' "$*"; }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }

COMPOSE="docker compose --progress quiet -f docker-compose.yml -f spanner/docker-compose.spanner.yml"
GRPCURL="$COMPOSE run --rm -T grpcurl -protoset /descriptors/spanner.pb"
CURL="$COMPOSE exec -T client curl"
DB="projects/demo/instances/demo-instance/databases/demodb"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Bring the overlay up first:" >&2
    echo "  ./run.sh && $COMPOSE up -d --wait" >&2
    exit 1
fi

# ------------------------------------------------------------------- setup
h "SETUP / instance and database via the emulator's REST admin"
note "curl to spanner:9020 from the client container. This hop bypasses the"
note "sidecar and Envoy: creating an instance is setup, and the admin"
note "gateway sits on a different port than the lane fronts. 409 on a"
note "re-run means 'already there', as good as 200."

code=$($CURL -s -o /dev/null -w '%{http_code}' --max-time 10 \
    -X POST http://spanner:9020/v1/projects/demo/instances \
    -H 'Content-Type: application/json' \
    -d '{"instanceId":"demo-instance","instance":{"config":"emulator-config","displayName":"demo","nodeCount":1}}')
[[ "$code" == "200" || "$code" == "409" ]] || fail "instance create answered HTTP $code"
ok "instance demo-instance (HTTP $code)"

code=$($CURL -s -o /dev/null -w '%{http_code}' --max-time 10 \
    -X POST http://spanner:9020/v1/projects/demo/instances/demo-instance/databases \
    -H 'Content-Type: application/json' \
    -d '{"createStatement":"CREATE DATABASE demodb","extraStatements":["CREATE TABLE songs (id INT64, title STRING(MAX)) PRIMARY KEY (id)"]}')
[[ "$code" == "200" || "$code" == "409" ]] || fail "database create answered HTTP $code"
ok "database demodb with table songs (HTTP $code)"

# ------------------------------------------------------------- tier 1 (OPA)
h "TIER 1 / the fat gate answers reachability, now for Spanner RPCs"
note "Spanner's API is gRPC, so Envoy is not blind here the way it is on"
note "the postgres lane: the same ext_authz filter runs and OPA sees only"
note "the method identity (:path /google.spanner.v1.Spanner/ListSessions)."
note "alice is granted spanner; bob is not, and is refused before any SQL"
note "exists."
note ""
note "  alice:"
out=$($GRPCURL -insecure -H 'x-hoop-user: alice' -d "{\"database\":\"$DB\"}" \
    envoy:8445 google.spanner.v1.Spanner/ListSessions 2>&1)
[[ $? -eq 0 ]] || { printf '%s\n' "$out" | sed 's/^/  /'; fail "alice could not reach the spanner lane through envoy:8445"; }
printf '%s\n' "$out" | sed 's/^/  /'
ok "alice reaches the lane through Envoy"
note ""
note "  bob (a 403 from OPA, before hoop-inspect or the emulator see anything):"
out=$($GRPCURL -insecure -H 'x-hoop-user: bob' -d "{\"database\":\"$DB\"}" \
    envoy:8445 google.spanner.v1.Spanner/ListSessions 2>&1)
rc=$?
printf '%s\n' "$out" | sed 's/^/  /' | head -4
[[ $rc -ne 0 ]] || fail "bob was let through; OPA should have refused"
ok "bob refused by the fat gate"

# ---------------------------------------------------------- tier 2 (the SQL)
h "TIER 2 / CreateSession, then ExecuteSql, through Envoy"
note "From here on the calls carry SQL. Envoy and OPA saw only the method"
note "identity above; the sidecar decodes the ExecuteSql payload against"
note "spanner.pb and extracts the GoogleSQL into the statement."
note ""
out=$($GRPCURL -insecure -H 'x-hoop-user: alice' -d "{\"database\":\"$DB\"}" \
    envoy:8445 google.spanner.v1.Spanner/CreateSession 2>&1)
SESSION=$(grep -o "\"$DB/sessions/[^\"]*\"" <<<"$out" | head -1 | tr -d '"')
[[ -n "$SESSION" ]] || { printf '%s\n' "$out" | sed 's/^/  /'; fail "CreateSession returned no session name"; }
ok "session ${SESSION##*/}"

out=$($GRPCURL -insecure -H 'x-hoop-user: alice' \
    -d "{\"session\":\"$SESSION\",\"sql\":\"SELECT 1\"}" \
    envoy:8445 google.spanner.v1.Spanner/ExecuteSql 2>&1)
[[ $? -eq 0 ]] || { printf '%s\n' "$out" | sed 's/^/  /'; fail "SELECT 1 should have succeeded"; }
printf '%s\n' "$out" | sed 's/^/  /'
ok "SELECT 1 crossed both hops and returned rows"

# --------------------------------------------------------------- guardrail
h "DENIED / a taxpayer id inside the SQL of an ExecuteSql request"
note "no-cpf-in-query is the process's one guardrail rule, a top-level"
note "default. The rule that refuses the pgwire DELETE, the HTTP query"
note "string and the grpc overlay's protobuf field also refuses a value in"
note "GoogleSQL text: the lane extracts the SQL into Statement.Text, where"
note "the pii scan reads it. The emulator never sees the frame."
note ""
out=$($GRPCURL -insecure -H 'x-hoop-user: alice' \
    -d "{\"session\":\"$SESSION\",\"sql\":\"SELECT * FROM songs WHERE cpf = '111.444.777-35'\"}" \
    envoy:8445 google.spanner.v1.Spanner/ExecuteSql 2>&1)
rc=$?
printf '%s\n' "$out" | sed 's/^/  /' | head -6
[[ $rc -ne 0 ]] || fail "the CPF statement should have been refused"
grep -q "PermissionDenied" <<<"$out" || fail "refused, but not with PermissionDenied"
grep -q "taxpayer id" <<<"$out" || fail "PermissionDenied without no-cpf-in-query's message"
ok "refused by no-cpf-in-query before the emulator saw the frame"

# ------------------------------------------------- the rule not afforded
h "ALLOWED / the rule this build cannot afford"
note "DELETE FROM songs reaches the emulator as this file ships."
note "no-destructive-googlesql, the operation rule that would refuse it by"
note "verb (lexer-derived: delete, drop, unknown), sits commented out in"
note "spanner/config-spanner.yaml, over the one-guardrail limit"
note "no-cpf-in-query already spends. Whatever the emulator answers below"
note "(a missing transaction, an empty table), the frame got through"
note "policy. Uncomment the rule and free the budget (comment out"
note "no-cpf-in-query, or set HOOP_LICENSE) and this beat flips to"
note "PermissionDenied."
note ""
out=$($GRPCURL -insecure -H 'x-hoop-user: alice' \
    -d "{\"session\":\"$SESSION\",\"sql\":\"DELETE FROM songs\"}" \
    envoy:8445 google.spanner.v1.Spanner/ExecuteSql 2>&1)
printf '%s\n' "$out" | sed 's/^/  /' | head -6
grep -q "PermissionDenied" <<<"$out" && fail "the DELETE was refused; no live rule should match it"
ok "the DELETE reached the emulator (no live rule matched)"

# ------------------------------------------------------------ without envoy
h "WITHOUT ENVOY / the same lane, direct, h2c"
note "The overlay publishes the lane on host port 29010, so this call skips"
note "TLS and the fat gate entirely: -plaintext, straight to hoop-inspect."
note "Policy and the audit trail are identical; the lane applies the same"
note "rules on both paths. The direct path drops Envoy's half: nothing"
note "authenticated this caller, so the x-hoop-user header is a claim"
note "anyone on the network can make. From the host:"
note "  grpcurl -plaintext -protoset ... localhost:29010 ..."
note "(../../gcloud-stack is the full Envoy-free stack built this way.)"
note ""
out=$($GRPCURL -plaintext -H 'x-hoop-user: mallory' \
    -d "{\"session\":\"$SESSION\",\"sql\":\"SELECT 1\"}" \
    hoop-inspect:29010 google.spanner.v1.Spanner/ExecuteSql 2>&1)
[[ $? -eq 0 ]] || { printf '%s\n' "$out" | sed 's/^/  /'; fail "the direct h2c call should have succeeded"; }
printf '%s\n' "$out" | sed 's/^/  /'
ok "mallory's direct call worked; nothing verified the header"

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
stats=$($CURL -s --max-time 10 http://hoop-inspect:19000/stats)
denied=0
for n in $(grep -o '"denied":[0-9]*' <<<"$stats" | cut -d: -f2); do
    denied=$((denied + n))
done
printf '  %s\n' "$stats"
[[ $denied -ge 1 ]] || fail "/stats shows no denials after the refused CPF statement"
ok "denied statements across all lanes: $denied"

note ""
note "The last spanner sessions, as an audit UI would read them:"
sessions=$($CURL -s --max-time 10 'http://hoop-inspect:19000/api/sessions?protocol=spanner&limit=5')
printf '%s\n' "$sessions" | sed -e 's/},{/},\n   {/g' -e 's/^/  /'
grep -q '"protocol":"spanner"' <<<"$sessions" \
    || fail "/api/sessions shows no sessions with protocol spanner"
ok "sessions carry protocol=spanner"

h "Summary"
cat <<'EOF'
  Envoy terminated TLS and forwarded a user header and ":path
  .../ExecuteSql". OPA read the same method identity and answered
  reachability, tier 1. hoop-inspect read the GoogleSQL text inside each
  ExecuteSql payload and refused the one carrying a taxpayer id. The
  emulator received only the frames policy let through, already inspected.

  The direct :29010 call produced the same verdicts and the same audit
  trail without Envoy. It also arrived unauthenticated: the header is a
  bare claim, which is why the base stack keeps data ports off the host
  and the lane's config marks identity_header as proxy-trust.
EOF
