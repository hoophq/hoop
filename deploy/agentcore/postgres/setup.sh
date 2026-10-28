#!/usr/bin/env bash
#
# Creates the template.yaml stack and asks its harness a question.
# README.md has the steps.
#
# Usage:
#   ./setup.sh create               deploy the stack
#   ./setup.sh session <question>   ask the harness; print its commands and answer
#   ./setup.sh delete               delete the stack
#
# create reads SIDECAR_TOKEN_FILE or LICENSE_FILE. Optional: AGENT (harness
# or none, default harness), MODEL_ID, AGENT_AZ_IDS (two AgentCore zone ids,
# required outside us-east-1), HARNESS_NAME (default: STACK with _ for -),
# IMAGE_CACHE_PREFIX (default: STACK in lower case). All commands read STACK
# (default hoop-agentcore-postgres) and the AWS CLI's profile and Region.
# Needs the AWS CLI v2, jq, python3, and curl 7.75 or later. session decodes
# the answer with ../http/evstream.py.

set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
stack=${STACK:-hoop-agentcore-postgres}

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }
for t in aws jq python3 curl; do command -v "$t" >/dev/null || die "missing required tool: $t"; done
region=${AWS_REGION:-${AWS_DEFAULT_REGION:-$(aws configure get region || true)}}
[[ -n "$region" ]] || die "no AWS Region: set AWS_REGION"
export AWS_REGION=$region

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
umask 077

output() {
    aws cloudformation describe-stacks --stack-name "$stack" \
        --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue" --output text
}

create() {
    local agent=${AGENT:-harness} cred_key cred_file
    if [[ -n "${SIDECAR_TOKEN_FILE:-}" ]]; then cred_key=HoopSidecarToken cred_file=$SIDECAR_TOKEN_FILE
    elif [[ -n "${LICENSE_FILE:-}" ]]; then cred_key=HoopLicense cred_file=$LICENSE_FILE
    else die "set SIDECAR_TOKEN_FILE (a use.hoop.dev sidecar token) or LICENSE_FILE"; fi
    [[ -s "$cred_file" ]] || die "$cred_file is missing or empty"
    [[ "$region" == us-east-1 || -n "${AGENT_AZ_IDS:-}" ]] \
        || die "set AGENT_AZ_IDS to two AgentCore zone ids of $region, such as euc1-az1,euc1-az2"

    local status prefix_list
    # A first create that failed leaves the stack, empty, in this state; a
    # deploy cannot update it.
    status=$(aws cloudformation describe-stacks --stack-name "$stack" --query 'Stacks[0].StackStatus' \
        --output text 2>/dev/null || true)
    [[ "$status" != ROLLBACK_COMPLETE ]] || die "stack $stack failed to create: run ./setup.sh delete, then create again"
    prefix_list=$(aws ec2 describe-managed-prefix-lists \
        --filters "Name=prefix-list-name,Values=com.amazonaws.$region.s3" \
        --query 'PrefixLists[0].PrefixListId' --output text)
    [[ "$prefix_list" == pl-* ]] || die "no S3 prefix list in $region"

    # A harness name is unique per Region, and a cache prefix per account
    # and Region, so each stack names its own.
    local harness_name cache_prefix
    harness_name=${HARNESS_NAME:-$(printf %s "$stack" | tr -c 'A-Za-z0-9' _ | cut -c1-40)}
    cache_prefix=${IMAGE_CACHE_PREFIX:-$(printf %s "$stack" | tr 'A-Z' 'a-z' | cut -c1-30 | sed -E 's/-+/-/g; s/-$//')}
    [[ ${#cache_prefix} -ge 2 ]] || die "cache prefix '$cache_prefix' is shorter than 2 characters: set IMAGE_CACHE_PREFIX"

    # A file, not argv: the process list would show the sidecar's credential.
    # An update keeps the old value of a parameter it does not name, so the
    # unused credential goes empty, not absent.
    jq -n --arg agent "$agent" --arg pl "$prefix_list" --arg harness "$harness_name" --arg cache "$cache_prefix" \
        --arg key "$cred_key" --rawfile cred "$cred_file" \
        --arg model "${MODEL_ID:-}" --arg azs "${AGENT_AZ_IDS:-}" \
        '{AgentKind: $agent, S3PrefixListId: $pl, HarnessName: $harness, ImageCachePrefix: $cache,
          HoopSidecarToken: "", HoopLicense: ""}
         + {($key): ($cred | rtrimstr("\n"))}
         + ({ModelId: $model, AgentAzIds: $azs} | with_entries(select(.value != "")))
         | to_entries | map({ParameterKey: .key, ParameterValue: .value})' > "$tmp/parameters.json"
    echo "creating stack $stack in $region; this takes about 9 minutes..."
    aws cloudformation deploy --stack-name "$stack" --template-file "$here/template.yaml" \
        --capabilities CAPABILITY_IAM --no-fail-on-empty-changeset \
        --parameter-overrides "file://$tmp/parameters.json"
    aws cloudformation describe-stacks --stack-name "$stack" --query 'Stacks[0].Outputs' --output table
}

# The harness answers in an event stream. A tool call, its result and the
# model's text each arrive in pieces; a block ends with contentBlockStop.
render() {
    jq -rs '
      reduce .[] as $e ({out: "", tool: "", result: "", text: ""};
        if $e.event == "contentBlockDelta" and $e.delta.toolUse then .tool += $e.delta.toolUse.input
        elif $e.event == "contentBlockDelta" and $e.delta.toolResult then
          .result += ([$e.delta.toolResult[].text // empty] | join(""))
        elif $e.event == "contentBlockStop" and .tool != "" then
          .out += "\n$ " + (.tool as $t | $t | fromjson? // {} | .command // $t) + "\n" | .tool = ""
        elif $e.event == "contentBlockStop" and .result != "" then
          .out += (.result as $r | $r | fromjson? // {stdout: $r}
                   | (.stdout // "") + (.stderr // "") + "[exit \(.exit_code // "?")]\n") | .result = ""
        elif $e.event == "contentBlockDelta" and $e.delta.text then .text += $e.delta.text
        elif $e.event == "messageStop" and .text != "" then .out += "\n[agent] " + .text + "\n" | .text = ""
        elif ($e.event // "" | test("Exception|Error$")) then .out += "\n[\($e.event)] \($e.message // $e)\n"
        else . end) | .out'
}

session() {
    local question=${1:?usage: ./setup.sh session <question>}
    local arn sid code
    arn=$(output HarnessArn)
    [[ -n "$arn" && "$arn" != None ]] || die "stack $stack has no harness: it was created with AGENT=none"
    # Not a pipe into grep -q: under pipefail, curl can die of SIGPIPE.
    [[ "$(curl --help all)" == *--aws-sigv4* ]] \
        || die "curl $(curl --version | head -1 | cut -d' ' -f2) has no --aws-sigv4: use 7.75 or later"
    sid=hoop-$(python3 -c 'import secrets; print(secrets.token_hex(16))')

    # The AWS CLI has no invoke-harness. The keys go to curl in a config on
    # stdin, not argv. The ARN goes in the query: some curl releases sign an
    # ARN in the path wrong.
    aws configure export-credentials --format process > "$tmp/creds.json"
    jq -r '"user = \"\(.AccessKeyId):\(.SecretAccessKey)\"",
           (.SessionToken // empty | "header = \"x-amz-security-token: \(.)\"")' "$tmp/creds.json" > "$tmp/curl.conf"
    rm -f "$tmp/creds.json"
    # The harness has no variable that names its session, so the first
    # message gives the id; the agent puts it in PGOPTIONS.
    jq -n --arg t "Your session id is $sid. $question" '{messages: [{role: "user", content: [{text: $t}]}]}' \
        > "$tmp/body.json"
    echo "session $sid: waiting for the agent..."
    code=$(curl -sS -m 600 -K "$tmp/curl.conf" --aws-sigv4 "aws:amz:$region:bedrock-agentcore" \
        -H "X-Amzn-Bedrock-AgentCore-Runtime-Session-Id: $sid" -H 'Content-Type: application/json' \
        --data-binary "@$tmp/body.json" -o "$tmp/stream" -w '%{http_code}' \
        "https://bedrock-agentcore.$region.amazonaws.com/harnesses/invoke?harnessArn=$(jq -rn --arg v "$arn" '$v|@uri')")
    [[ "$code" == 200 ]] || die "InvokeHarness: HTTP $code: $(cat "$tmp/stream")"
    python3 "$here/../http/evstream.py" < "$tmp/stream" > "$tmp/events.jsonl"
    render < "$tmp/events.jsonl"
    printf '\nsession %s\n' "$sid"
    if jq -se 'any(.event // "" | test("Exception|Error$"))' "$tmp/events.jsonl" >/dev/null; then
        die "the session ended with an error"
    fi
}

delete() {
    aws cloudformation delete-stack --stack-name "$stack"
    echo "deleting stack $stack..."
    aws cloudformation wait stack-delete-complete --stack-name "$stack" \
        || die "stack $stack did not delete. AgentCore can hold network interfaces in the agent subnets for hours; run ./setup.sh delete again later"
    echo "deleted stack $stack"
}

case "${1:-}" in
    create) create ;;
    session) session "${@:2}" ;;
    delete) delete ;;
    *) die "usage: ./setup.sh create | session <question> | delete" ;;
esac
