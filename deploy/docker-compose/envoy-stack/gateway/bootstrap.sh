#!/bin/sh
#
# Prepares the gateway for the review lane, then hands the sidecar its
# credential.
#
# Everything here goes through the public REST API, on purpose: it is the same
# surface an operator would use, so a step that breaks here breaks for them
# too. Seeding SQL directly would hide exactly the failures worth catching (a
# required field, a role, a validation rule).
#
# Idempotent. Re-running against a live stack re-reads what exists rather than
# failing, so ./run.sh --review can be run twice.
#
# Runs as a one-shot compose service and writes /secrets/review-token, which
# hoop-inspect mounts. It writes the file as uid 10001 (the sidecar's user)
# because analyzer.ReadSecretFile refuses anything group- or world-readable,
# and a bind mount from the host cannot promise that on every platform.

set -eu

API="http://hoop-gateway:8009/api"
ADMIN_EMAIL="${HOOP_ADMIN_EMAIL:-admin@hoop.dev}"
ADMIN_PASSWORD="${HOOP_ADMIN_PASSWORD:-hoopdemo123}"
CONNECTION="${HOOP_CONNECTION:-appdb}"
AGENT_NAME="inspect-placeholder"
AIAGENT_NAME="sandbox-appdb"
RULE_NAME="appdb-statement-review"
INSPECT_UID=10001

step() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
die()  { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || apk add --no-cache curl jq >/dev/null 2>&1
command -v jq   >/dev/null 2>&1 || apk add --no-cache jq   >/dev/null 2>&1

# ------------------------------------------------------------------ 1. admin
# Local auth lets exactly one user register: the first one becomes the org
# admin and everyone after gets a 409. So a 409 means "already bootstrapped",
# and we log in instead. Both endpoints return the token in a Token header
# rather than the body.
step "admin user"
TOKEN=$(curl -sS -D - -o /dev/null -X POST "$API/localauth/register" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\",\"name\":\"Reviewer\"}" \
    | awk 'tolower($1) == "token:" { print $2 }' | tr -d '\r')

if [ -z "$TOKEN" ]; then
    TOKEN=$(curl -sS -D - -o /dev/null -X POST "$API/localauth/login" \
        -H 'Content-Type: application/json' \
        -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" \
        | awk 'tolower($1) == "token:" { print $2 }' | tr -d '\r')
    [ -n "$TOKEN" ] || die "could not register or log in as $ADMIN_EMAIL"
    ok "logged in as $ADMIN_EMAIL (already registered)"
else
    ok "registered $ADMIN_EMAIL as the org admin"
fi

AUTH="Authorization: Bearer $TOKEN"
api() { curl -sS -H "$AUTH" -H 'Content-Type: application/json' "$@"; }

# ------------------------------------------------------------------ 2. agent
# A connection must name an agent, and this stack deliberately runs none:
# hoop-inspect IS the data path here, and no hoop session ever opens against
# this connection. The record exists so the connection is well formed and the
# review can point at something a reviewer recognizes.
step "placeholder agent"
agent_id() {
    api "$API/agents" | jq -r --arg n "$AGENT_NAME" '.[] | select(.name == $n) | .id' | head -1
}
AGENT_ID=$(agent_id)
if [ -z "$AGENT_ID" ] || [ "$AGENT_ID" = "null" ]; then
    # POST /agents answers with the DSN and nothing else — the id is not in
    # the response — so the record is looked up afterwards either way.
    api -X POST "$API/agents" -d "{\"name\":\"$AGENT_NAME\",\"mode\":\"standard\"}" >/dev/null
    AGENT_ID=$(agent_id)
    [ -n "$AGENT_ID" ] && [ "$AGENT_ID" != "null" ] || die "could not create the agent record"
    ok "created agent $AGENT_NAME"
else
    ok "reusing agent $AGENT_NAME"
fi

# ------------------------------------------------------------- 3. connection
# The name must match the lane's `connection:` in config.yaml. That string is
# what the sidecar sends on the claim and what scopes the approval, so an
# approval for appdb cannot authorize the same SQL somewhere else.
step "connection $CONNECTION"
if api "$API/connections/$CONNECTION" | jq -e '.id' >/dev/null 2>&1; then
    ok "reusing connection $CONNECTION"
else
    # The access_mode_* fields are required by the API and mean nothing here:
    # no hoop session ever opens against this connection, because hoop-inspect
    # is the data path. They are set to "disabled" to say so out loud.
    api -X POST "$API/connections" -d "{
        \"name\": \"$CONNECTION\",
        \"type\": \"database\",
        \"subtype\": \"postgres\",
        \"agent_id\": \"$AGENT_ID\",
        \"command\": [],
        \"secret\": {},
        \"access_mode_runbooks\": \"disabled\",
        \"access_mode_exec\": \"disabled\",
        \"access_mode_connect\": \"disabled\",
        \"access_schema\": \"disabled\"
    }" | jq -e '.id' >/dev/null || die "could not create connection $CONNECTION"
    ok "created connection $CONNECTION"
fi

# ------------------------------------------------------------ 4. review rule
# Who answers a review is the org's existing access-request configuration, not
# a second approval system. reviewers_groups is the load-bearing field: the
# gateway refuses to file a review for a connection whose rule names nobody.
step "access request rule"
if api "$API/access-requests/rules/$RULE_NAME" | jq -e '.name' >/dev/null 2>&1; then
    ok "reusing rule $RULE_NAME"
else
    api -X POST "$API/access-requests/rules" -d "{
        \"name\": \"$RULE_NAME\",
        \"description\": \"Statements hoop-inspect held for a human on $CONNECTION\",
        \"access_type\": \"command\",
        \"connection_names\": [\"$CONNECTION\"],
        \"approval_required_groups\": [],
        \"reviewers_groups\": [\"admin\"],
        \"force_approval_groups\": [\"admin\"],
        \"all_groups_must_approve\": false,
        \"min_approvals\": 1
    }" | jq -e '.name' >/dev/null || die "could not create the access request rule"
    ok "created rule $RULE_NAME (reviewers: admin)"
fi

# --------------------------------------------------------------- 5. sandbox
# The hpk_ token IS the sandbox. It is bound to a named environment rather
# than to a human it acts for, it carries its own groups, and it owns the
# reviews it files — so a reviewer reading the queue sees which environment
# asked. The full key is shown exactly once, at creation, which is why an
# existing agent is revoked and re-minted rather than reused.
step "sandbox credential"
mkdir -p /secrets

# Reuse a token that still works. Re-minting on every run would be tidier to
# write and wrong to ship: the sidecar reads this file once at startup, so
# rotating it underneath a running sidecar leaves it holding a revoked
# credential and every claim answering 401 — a review gate that refuses
# everything, for a reason nothing in its logs explains.
KEY=""
if [ -s /secrets/review-token ]; then
    CANDIDATE=$(cat /secrets/review-token)
    CODE=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $CANDIDATE" \
        "$API/relay/reviews?connection=$CONNECTION&statement_hash=$(printf 0%.0s $(seq 64))")
    # 404 is the healthy answer here: authenticated, authorized for this
    # connection, and no review for a hash of all zeros. 401/403 means the
    # credential is gone.
    if [ "$CODE" = "404" ] || [ "$CODE" = "200" ]; then
        KEY="$CANDIDATE"
        ok "reusing the existing $AIAGENT_NAME token (still valid)"
    fi
fi

if [ -z "$KEY" ]; then
    # `.[]?` because the list endpoint answers null rather than [] when empty.
    EXISTING=$(api "$API/ai-agents" | jq -r --arg n "$AIAGENT_NAME" '.[]? | select(.name == $n) | .id' | head -1)
    if [ -n "$EXISTING" ] && [ "$EXISTING" != "null" ]; then
        api -X DELETE "$API/ai-agents/$AIAGENT_NAME" >/dev/null
        ok "revoked the stale $AIAGENT_NAME key (the full key is only shown once)"
    fi
    KEY=$(api -X POST "$API/ai-agents" -d "{\"name\":\"$AIAGENT_NAME\",\"groups\":[\"admin\"]}" | jq -r '.key')
    [ -n "$KEY" ] && [ "$KEY" != "null" ] || die "could not mint an hpk_ token"

    printf '%s' "$KEY" > /secrets/review-token
    chmod 600 /secrets/review-token
    chown "$INSPECT_UID" /secrets/review-token
    ok "wrote /secrets/review-token (0600, uid $INSPECT_UID)"
fi

# The demo script needs the same token to poll as the sandbox, and the admin
# token to approve. Neither is a secret worth protecting in a throwaway stack,
# but they are still kept out of the compose logs.
printf '%s' "$TOKEN" > /secrets/admin-token
chmod 644 /secrets/admin-token

printf '\n\033[1madmin\033[0m %s / %s\n' "$ADMIN_EMAIL" "$ADMIN_PASSWORD"
printf '\033[1msandbox\033[0m %s… (hpk_, mounted into hoop-inspect)\n\n' "$(echo "$KEY" | cut -c1-12)"
