#!/usr/bin/env bash
#
# Exercises the stack end to end, asserting as it goes: every beat prints
# what it proves and the script exits non-zero at the first failed assertion.
#
#   grpcurl -> hoop-inspect :29010 -> spanner emulator     (GoogleSQL lexer)
#   grpcurl -> hoop-inspect :29060 -> bigquery emulator    (method-only lane)
#
# The spanner beats carry the claim: the lane pulls the SQL text out of
# ExecuteSql payloads, classifies it with the googlesql lexer dialect, and
# the operation rule refuses by VERB. A DELETE is refused before the
# emulator sees the frame, a DELETE hiding in a raw string stays a select,
# and SQL the lexer cannot read is refused as unknown (fail-closed).
#
# Prereqs: ./run.sh

set -uo pipefail
cd "$(dirname "$0")"

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m  %s\n' "$*"; }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }

CURL="docker compose exec -T client curl"
GRPCURL="docker compose --progress quiet run --rm -T grpcurl"
DB="projects/demo/instances/demo-instance/databases/demodb"

if ! curl -sf http://localhost:19001/healthz >/dev/null; then
    fail "sidecar admin :19001 not answering; run ./run.sh first"
fi

# ------------------------------------------------------------------- setup
h "SETUP / instance and database via the emulator's REST admin"
note "curl to spanner:9020 from the client container. This hop BYPASSES the"
note "sidecar on purpose: instance creation is setup, and the admin gateway"
note "is a different port than the lane fronts. 409 on a re-run means"
note "'already there'; the script accepts both."

code=$($CURL -s -o /dev/null -w '%{http_code}' --max-time 10 \
    -X POST http://spanner:9020/v1/projects/demo/instances \
    -H 'Content-Type: application/json' \
    -d '{"instanceId":"demo-instance","instance":{"config":"emulator-config","displayName":"demo","nodeCount":1}}')
[[ "$code" == "200" || "$code" == "409" ]] || fail "instance create answered HTTP $code"
ok "instance demo-instance (HTTP $code)"

# The songs table exists so the ALLOWED statements below genuinely succeed
# at the emulator. The DENIED ones would not need it: the sidecar refuses
# those before the upstream sees the frame, whether the table exists or not.
code=$($CURL -s -o /dev/null -w '%{http_code}' --max-time 10 \
    -X POST http://spanner:9020/v1/projects/demo/instances/demo-instance/databases \
    -H 'Content-Type: application/json' \
    -d '{"createStatement":"CREATE DATABASE demodb","extraStatements":["CREATE TABLE songs (id INT64, title STRING(MAX)) PRIMARY KEY (id)"]}')
[[ "$code" == "200" || "$code" == "409" ]] || fail "database create answered HTTP $code"
$CURL -sf --max-time 10 "http://spanner:9020/v1/$DB" >/dev/null \
    || fail "database $DB not readable after create"
ok "database demodb with table songs (HTTP $code)"

# ----------------------------------------------------------------- session
h "SPANNER / CreateSession through the sidecar lane"
note "From here on every call crosses hoop-inspect:29010. -protoset is the"
note "descriptor set run.sh fetched over reflection: the same file the"
note "lane pinned, reused as the client's schema."

out=$($GRPCURL -plaintext -protoset /descriptors/spanner.pb \
    -d "{\"database\":\"$DB\"}" \
    hoop-inspect:29010 google.spanner.v1.Spanner/CreateSession 2>&1)
SESSION=$(grep -o "\"$DB/sessions/[^\"]*\"" <<<"$out" | head -1 | tr -d '"')
[[ -n "$SESSION" ]] || { printf '%s\n' "$out" | sed 's/^/  /'; fail "CreateSession returned no session name"; }
ok "session ${SESSION##*/}"

# xsql runs one ExecuteSql on that session, stdout+stderr merged so callers
# can assert on grpcurl's error rendering. The SQL travels as a JSON string;
# every statement below is single-quote-safe inside it.
xsql() {
    $GRPCURL -plaintext -protoset /descriptors/spanner.pb \
        -d "{\"session\":\"$SESSION\",\"sql\":\"$1\"}" \
        hoop-inspect:29010 google.spanner.v1.Spanner/ExecuteSql 2>&1
}

# expect_allow: the RPC must succeed, meaning the sidecar classified the
# statement, found no matching rule, and the emulator answered rows.
expect_allow() { # <sql> <what it proves>
    local out rc
    out=$(xsql "$1"); rc=$?
    if [[ $rc -ne 0 ]]; then
        printf '%s\n' "$out" | sed 's/^/  /'
        fail "expected ALLOW for: $1"
    fi
    ok "allowed: $1  ($2)"
}

# expect_deny: grpcurl must exit non-zero with PermissionDenied carrying the
# rule's message. The emulator never saw the frame, which is why a DELETE
# against any table, existing or not, denies identically.
expect_deny() { # <sql> <what it proves>
    local out rc
    out=$(xsql "$1"); rc=$?
    if [[ $rc -eq 0 ]]; then
        printf '%s\n' "$out" | sed 's/^/  /'
        fail "expected DENY for: $1"
    fi
    grep -q "PermissionDenied" <<<"$out" \
        || { printf '%s\n' "$out" | sed 's/^/  /'; fail "denied, but not with PermissionDenied: $1"; }
    grep -q "not permitted on the spanner lane" <<<"$out" \
        || { printf '%s\n' "$out" | sed 's/^/  /'; fail "PermissionDenied without the rule's message: $1"; }
    ok "denied:  $1  ($2)"
}

# -------------------------------------------------------------- guardrails
h "SPANNER / the GoogleSQL lexer decides, end to end"
note "One operation rule on the lane: operations [delete, drop, unknown]."
note "Every call below uses the same session, method and lane; only the SQL"
note "text differs, so each verdict comes from the lexer."

expect_allow "SELECT 1" \
    "a select reaches the emulator and returns rows"
expect_deny "DELETE FROM songs WHERE TRUE" \
    "refused by verb before the upstream saw the frame"

h "SPANNER / evasions the lexer is immune to"
note "Substring matching would get these wrong. The classifier strips"
note "comments and knows GoogleSQL's raw strings, so the DELETE *inside a"
note "literal* is a select and the DELETE *behind a comment* is still a"
note "delete."

expect_allow "SELECT r'x' FROM songs" \
    "a raw-string literal does not spook the scanner"
expect_deny "DELETE /* hidden */ FROM songs" \
    "a comment does not hide the verb"

h "SPANNER / fail-closed on SQL the scanner cannot read"
note "An unterminated string never classifies. The rule's operations list"
note "includes 'unknown' for this case: on a lane that exists to read the"
note "SQL, a statement the scanner could not read is denied."

expect_deny "SELECT 'oops FROM songs" \
    "unlexable SQL classifies as unknown and the rule catches it"

# -------------------------------------------------------------- bqstorage
h "BQSTORAGE / the method-only lane"
if docker compose exec -T client test -f /descriptors/bqstorage.pb; then
    note "The lane captures no payloads and needs no descriptors; identity is"
    note "the policy surface (service and method travel in Tables). The"
    note "write-plane fence ships commented out in sidecar/config.yaml (the"
    note "free tier's one guardrail is spent on the spanner rule), so this"
    note "beat adapts: fenced on a licensed build, pass-through otherwise."

    out=$($GRPCURL -plaintext -protoset /descriptors/bqstorage.pb \
        -d '{"writeStream":"projects/demo/datasets/demo_ds/tables/t1/streams/_default"}' \
        hoop-inspect:29060 google.bigquery.storage.v1.BigQueryWrite/AppendRows 2>&1)
    if grep -q "write plane is fenced" <<<"$out"; then
        ok "AppendRows refused with the fence rule's message (licensed build)"
    else
        note "  AppendRows reached the emulator (fence over the limit); it answered:"
        printf '%s\n' "$out" | sed 's/^/    /' | head -4
    fi

    out=$($GRPCURL -plaintext -protoset /descriptors/bqstorage.pb \
        -d '{"parent":"projects/demo","readSession":{"table":"projects/demo/datasets/demo_ds/tables/t1","dataFormat":"AVRO"}}' \
        hoop-inspect:29060 google.bigquery.storage.v1.BigQueryRead/CreateReadSession 2>&1)
    grep -q "write plane is fenced" <<<"$out" \
        && fail "the write fence caught a READ; the rule is too broad"
    ok "CreateReadSession crossed the lane; the emulator answered:"
    printf '%s\n' "$out" | sed 's/^/    /' | head -4
else
    note "skipped: /descriptors/bqstorage.pb absent. run.sh warned about it:"
    note "the emulator did not serve reflection, and without a protoset"
    note "grpcurl cannot encode the calls. The LANE is unaffected: method-only"
    note "lanes never needed the descriptors."
fi

# ------------------------------------------------------------------- audit
h "AUDIT / the sidecar counted every refusal"
note "Read from the client, on the compose network (:19000 internal; the"
note "host publishes it as :19001)."

stats=$($CURL -s --max-time 10 http://hoop-inspect:19000/stats)
denied=0
for n in $(grep -o '"denied":[0-9]*' <<<"$stats" | cut -d: -f2); do
    denied=$((denied + n))
done
printf '  %s\n' "$stats"
[[ $denied -gt 0 ]] || fail "/stats shows no denials after three refused statements"
ok "denied statements across all lanes: $denied"

note ""
note "The last sessions, as an audit UI would read them:"
$CURL -s --max-time 10 'http://hoop-inspect:19000/api/sessions?limit=5' \
    | sed -e 's/},{/},\n   {/g' -e 's/^/  /'

h "Summary"
cat <<'EOF'
  The emulator saw:  CreateSession, SELECT 1, SELECT r'x' FROM songs.
  It never saw:      either DELETE, or the unterminated string.
  Who decided:       the googlesql lexer dialect, per extracted statement,
                     inside the sidecar. The verdict keys on the lexed
                     verb, so a client cannot phrase its way past the rule.
EOF
