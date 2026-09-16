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
note "DELETE FROM songs WHERE true reaches the emulator as this file ships."
note "(WHERE true, because the emulator refuses a DELETE with no WHERE.)"
note "no-destructive-googlesql, the operation rule that would refuse it by"
note "verb (lexer-derived: delete, drop, unknown), sits commented out in"
note "spanner/config-spanner.yaml, over the one-guardrail limit"
note "no-cpf-in-query already spends. The call carries a read-write"
note "transaction so the DELETE is valid DML: the beat asserts the RPC"
note "SUCCEEDED, not merely that no denial string appeared. Uncomment the"
note "rule and free the budget (comment out no-cpf-in-query, or set"
note "HOOP_LICENSE) and this beat flips to PermissionDenied."
note ""
out=$($GRPCURL -insecure -H 'x-hoop-user: alice' \
    -d "{\"session\":\"$SESSION\",\"transaction\":{\"begin\":{\"readWrite\":{}}},\"sql\":\"DELETE FROM songs WHERE true\"}" \
    envoy:8445 google.spanner.v1.Spanner/ExecuteSql 2>&1)
rc=$?
printf '%s\n' "$out" | sed 's/^/  /' | head -6
# "Reached the emulator" is a claim about the RPC, not about the absence of
# one string: a dead emulator or a broken session also answers without
# PermissionDenied, and reading that as a pass proves nothing crossed.
if [[ $rc -ne 0 ]]; then
    printf '%s\n' "$out" | sed 's/^/  /'
    fail "the DELETE failed (exit $rc); it should have reached the emulator and succeeded"
fi
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

# --------------------------------------------------------------- dialects
h "DIALECTS / the same quoted query on a GoogleSQL and a PostgreSQL database"
note "A Spanner database is created as GoogleSQL or as the PostgreSQL"
note "interface, and ExecuteSql never says which. config-spanner.yaml maps"
note "demodb-pg to postgresql and leaves demodb on the lane default. The"
note "query below quotes its table: in PostgreSQL \"songs\" is an identifier,"
note "in GoogleSQL it is a string literal. The lane must record tables=[songs]"
note "on demodb-pg and NOT on demodb, and say which lexer it used."
note ""
PGDB="projects/demo/instances/demo-instance/databases/demodb-pg"
code=$($CURL -s -o /dev/null -w '%{http_code}' --max-time 20 \
    -X POST http://spanner:9020/v1/projects/demo/instances/demo-instance/databases \
    -H 'Content-Type: application/json' \
    -d '{"createStatement":"CREATE DATABASE \"demodb-pg\"","databaseDialect":"POSTGRESQL","extraStatements":["CREATE TABLE songs (id bigint PRIMARY KEY, title varchar)"]}')
[[ "$code" == "200" || "$code" == "409" ]] || fail "PostgreSQL-dialect database create answered HTTP $code"
ok "database demodb-pg, dialect POSTGRESQL (HTTP $code)"

out=$($GRPCURL -insecure -H 'x-hoop-user: alice' -d "{\"database\":\"$PGDB\"}" \
    envoy:8445 google.spanner.v1.Spanner/CreateSession 2>&1)
PGSESSION=$(grep -o "\"$PGDB/sessions/[^\"]*\"" <<<"$out" | head -1 | tr -d '"')
[[ -n "$PGSESSION" ]] || { printf '%s\n' "$out" | sed 's/^/  /'; fail "CreateSession on demodb-pg returned no session name"; }
ok "session ${PGSESSION##*/} on demodb-pg"

MARK="dialect-$RANDOM$RANDOM"
QUERY="SELECT id, title FROM \\\"songs\\\" WHERE title = '$MARK'"
note ""
note "  on demodb-pg (postgresql):"
out=$($GRPCURL -insecure -H 'x-hoop-user: alice' \
    -d "{\"session\":\"$PGSESSION\",\"sql\":\"$QUERY\"}" \
    envoy:8445 google.spanner.v1.Spanner/ExecuteSql 2>&1)
[[ $? -eq 0 ]] || { printf '%s\n' "$out" | sed 's/^/  /'; fail "the quoted query should be valid PostgreSQL and succeed"; }
ok "the emulator ran it: \"songs\" is an identifier there"
note ""
note "  on demodb (googlesql; the emulator refuses it, the lane still records it):"
out=$($GRPCURL -insecure -H 'x-hoop-user: alice' \
    -d "{\"session\":\"$SESSION\",\"sql\":\"$QUERY\"}" \
    envoy:8445 google.spanner.v1.Spanner/ExecuteSql 2>&1)
printf '%s\n' "$out" | sed 's/^/  /' | head -3
grep -q "PermissionDenied" <<<"$out" && fail "the lane refused the query; no live rule should match it"

sleep 1
events=$($CURL -s --max-time 10 "http://hoop-inspect:19000/api/events?protocol=spanner&q=$MARK&limit=20")
# One line per statement event: <database> <dialect> <tables>, read from the
# event's metadata and tables fields.
read_events() {
    python3 -c '
import json, sys
for e in json.load(sys.stdin).get("events", []):
    if e.get("kind") != "statement": continue
    m = e.get("metadata", {})
    print(m.get("spanner.database", "-"), m.get("spanner.dialect", "-"), ",".join(e.get("tables", [])) or "-")
' <<<"$events"
}
rows=$(read_events)
printf '%s\n' "$rows" | sed 's/^/  /'
pg_row=$(grep "^$PGDB " <<<"$rows" | head -1)
gsql_row=$(grep "^$DB " <<<"$rows" | head -1)
[[ -n "$pg_row" && -n "$gsql_row" ]] || fail "/api/events lacks a statement for each database"
[[ "$pg_row" == "$PGDB postgresql songs" ]] || fail "demodb-pg: want 'postgresql songs', got '${pg_row#"$PGDB" }'"
[[ "$gsql_row" == "$DB googlesql -" ]] || fail "demodb: want 'googlesql -' (a string, not a table), got '${gsql_row#"$DB" }'"
ok "demodb-pg read as postgresql with tables=[songs]; demodb read as googlesql with none"
ok "a table rule fencing songs fires on the PostgreSQL database and cannot be dodged by quoting"

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
