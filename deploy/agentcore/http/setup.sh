#!/usr/bin/env bash
#
# Creates the template.yaml stack, asks its harness a question, and runs
# runtime-example/ as a Runtime agent on its network. README.md has the steps.
#
# Usage:
#   ./setup.sh create                       deploy the stack
#   ./setup.sh session <question>           ask the harness; print its commands and answer
#   ./setup.sh runtime create               build runtime-example/, create it on the stack's network
#   ./setup.sh runtime session <question>   ask the Runtime agent
#   ./setup.sh runtime delete               delete the Runtime agent
#   ./setup.sh delete                       delete the Runtime agent, then the stack
#
# create reads UPSTREAM_URL, and SIDECAR_TOKEN_FILE or LICENSE_FILE. Optional:
# AGENT (harness or none, default harness), API_TOKEN_SECRET_ARN, MODEL_ID,
# AGENT_AZ_IDS (two AgentCore zone ids, required outside us-east-1),
# HARNESS_NAME (default: STACK with _ for -). All commands read STACK
# (default hoop-agentcore-http) and the AWS CLI's profile and Region. The
# Runtime agent is named after STACK, and its ARN goes to AGENTCORE_IDS,
# default ./.agentcore-ids-<STACK>.
# Needs the AWS CLI v2, jq, python3, and curl 7.75 or later; runtime create
# also needs docker with buildx.

set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
stack=${STACK:-hoop-agentcore-http}
ids=${AGENTCORE_IDS:-.agentcore-ids-$stack}
runtime_name=$(printf %s "$stack" | tr -c 'A-Za-z0-9' _ | cut -c1-36)_runtime

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
parameter() {
    aws cloudformation describe-stacks --stack-name "$stack" \
        --query "Stacks[0].Parameters[?ParameterKey=='$1'].ParameterValue" --output text
}
secret() { aws secretsmanager get-secret-value --secret-id "$1" --query SecretString --output text; }
new_session() { printf 'hoop-%s\n' "$(python3 -c 'import secrets; print(secrets.token_hex(16))')"; }

create() {
    : "${UPSTREAM_URL:?set UPSTREAM_URL to the REST API, such as https://api.example.com}"
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

    # A harness name is unique per Region, so each stack names its own.
    local harness_name
    harness_name=${HARNESS_NAME:-$(printf %s "$stack" | tr -c 'A-Za-z0-9' _ | cut -c1-40)}

    # A file, not argv: the process list would show the sidecar's credential.
    # An update keeps the old value of a parameter it does not name, so the
    # unused credential and the API token go empty, not absent.
    jq -n --arg agent "$agent" --arg url "$UPSTREAM_URL" --arg pl "$prefix_list" --arg harness "$harness_name" \
        --arg key "$cred_key" --rawfile cred "$cred_file" \
        --arg api "${API_TOKEN_SECRET_ARN:-}" --arg model "${MODEL_ID:-}" --arg azs "${AGENT_AZ_IDS:-}" \
        '{AgentKind: $agent, UpstreamUrl: $url, S3PrefixListId: $pl, HarnessName: $harness,
          HoopSidecarToken: "", HoopLicense: "", ApiTokenSecretArn: $api}
         + {($key): ($cred | rtrimstr("\n"))}
         + ({ModelId: $model, AgentAzIds: $azs} | with_entries(select(.value != "")))
         | to_entries | map({ParameterKey: .key, ParameterValue: .value})' > "$tmp/parameters.json"
    echo "creating stack $stack in $region; this takes about 8 minutes..."
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
    sid=$(new_session)

    # The AWS CLI has no invoke-harness. The keys go to curl in a config on
    # stdin, not argv. The ARN goes in the query: some curl releases sign an
    # ARN in the path wrong.
    aws configure export-credentials --format process > "$tmp/creds.json"
    jq -r '"user = \"\(.AccessKeyId):\(.SecretAccessKey)\"",
           (.SessionToken // empty | "header = \"x-amz-security-token: \(.)\"")' "$tmp/creds.json" > "$tmp/curl.conf"
    rm -f "$tmp/creds.json"
    # The harness has no variable that names its session, so the first
    # message gives the id; the agent sends it on every request.
    jq -n --arg t "Your session id is $sid. $question" '{messages: [{role: "user", content: [{text: $t}]}]}' \
        > "$tmp/body.json"
    echo "session $sid: waiting for the agent..."
    code=$(curl -sS -m 600 -K "$tmp/curl.conf" --aws-sigv4 "aws:amz:$region:bedrock-agentcore" \
        -H "X-Amzn-Bedrock-AgentCore-Runtime-Session-Id: $sid" -H 'Content-Type: application/json' \
        --data-binary "@$tmp/body.json" -o "$tmp/stream" -w '%{http_code}' \
        "https://bedrock-agentcore.$region.amazonaws.com/harnesses/invoke?harnessArn=$(jq -rn --arg v "$arn" '$v|@uri')")
    [[ "$code" == 200 ]] || die "InvokeHarness: HTTP $code: $(cat "$tmp/stream")"
    python3 "$here/evstream.py" < "$tmp/stream" > "$tmp/events.jsonl"
    render < "$tmp/events.jsonl"
    printf '\nsession %s\n' "$sid"
    if jq -se 'any(.event // "" | test("Exception|Error$"))' "$tmp/events.jsonl" >/dev/null; then
        die "the session ended with an error"
    fi
}

runtime_create() {
    command -v docker >/dev/null || die "missing required tool: docker"
    [[ ! -s "$ids" ]] || die "$ids already names a Runtime agent: run ./setup.sh runtime delete first"
    local repo image arn api_arn
    repo=$(output AgentRepositoryUri)
    image="$repo:runtime-example"
    aws ecr get-login-password | docker login --username AWS --password-stdin "${repo%%/*}" >/dev/null
    docker buildx build --platform linux/arm64 --provenance=false -t "$image" --push "$here/runtime-example"

    # Files, not argv: the tokens go into the agent's environment variables.
    secret "$(output CallerTokenSecretArn)" > "$tmp/caller"
    api_arn=$(parameter ApiTokenSecretArn)
    if [[ -n "$api_arn" ]]; then secret "$api_arn" > "$tmp/api"; else : > "$tmp/api"; fi
    jq -n --arg name "$runtime_name" --arg image "$image" --arg role "$(output AgentRoleArn)" \
        --arg subnets "$(output AgentSubnetIds)" --arg sg "$(output AgentSecurityGroupId)" \
        --arg url "$(output SidecarUrl)" --arg model "$(parameter ModelId)" --arg region "$region" \
        --rawfile caller "$tmp/caller" --rawfile api "$tmp/api" \
        '{agentRuntimeName: $name, roleArn: $role,
          agentRuntimeArtifact: {containerConfiguration: {containerUri: $image}},
          networkConfiguration: {networkMode: "VPC",
            networkModeConfig: {subnets: ($subnets | split(",")), securityGroups: [$sg]}},
          environmentVariables: ({SIDECAR_URL: $url, HOOP_CALLER_TOKEN: ($caller | rtrimstr("\n")),
                                  MODEL_ID: $model, AWS_REGION: $region}
            + if $api == "" then {} else {CUSTOMER_API_TOKEN: ($api | rtrimstr("\n"))} end)}' > "$tmp/runtime.json"
    arn=$(aws bedrock-agentcore-control create-agent-runtime --cli-input-json "file://$tmp/runtime.json" \
        --query agentRuntimeArn --output text)
    printf 'runtime %s\n' "$arn" > "$ids"
    echo "runtime agent $arn: waiting until it is ready..."
    local status=CREATING
    for _ in $(seq 60); do
        status=$(aws bedrock-agentcore-control get-agent-runtime --agent-runtime-id "${arn##*/}" \
            --query status --output text)
        [[ "$status" == *ING ]] || break
        sleep 5
    done
    [[ "$status" == READY ]] || die "runtime agent status: $status"
    echo "runtime agent ready"
}

runtime_arn() { awk '$1 == "runtime" {print $2}' "$ids" 2>/dev/null || true; }

runtime_session() {
    local question=${1:?usage: ./setup.sh runtime session <question>}
    local arn sid
    arn=$(runtime_arn)
    [[ -n "$arn" ]] || die "no Runtime agent in $ids: run ./setup.sh runtime create first"
    sid=$(new_session)
    jq -n --arg p "$question" '{prompt: $p}' > "$tmp/payload.json"
    echo "session $sid: waiting for the agent..."
    aws bedrock-agentcore invoke-agent-runtime --agent-runtime-arn "$arn" --runtime-session-id "$sid" \
        --content-type application/json --accept application/json \
        --payload "fileb://$tmp/payload.json" "$tmp/answer.json" >/dev/null
    jq -r '(.steps[]? | "\n> \(.method) \(.url)\n\(if .error then "error: \(.error)"
             else "\(.status)\(if .denied then " x-hoop-denied: \(.denied)" else "" end)\n\(.body)" end)"),
           "\n[agent] \(.answer // .error)"' "$tmp/answer.json"
    printf '\nsession %s\n' "$sid"
    if jq -e '.error' "$tmp/answer.json" >/dev/null; then die "the agent answered an error"; fi
}

runtime_delete() {
    local arn status=DELETING out
    arn=$(runtime_arn)
    [[ -n "$arn" ]] || { echo "no Runtime agent in $ids"; return; }
    aws bedrock-agentcore-control delete-agent-runtime --agent-runtime-id "${arn##*/}" >/dev/null || true
    # Its network interfaces hold the stack's subnets until it is gone.
    for _ in $(seq 60); do
        if ! out=$(aws bedrock-agentcore-control get-agent-runtime --agent-runtime-id "${arn##*/}" \
            --query status --output text 2>&1); then
            [[ "$out" == *ResourceNotFound* ]] || die "get-agent-runtime: $out"
            status=GONE
            break
        fi
        status=$out
        [[ "$status" == DELETING ]] || die "runtime agent status: $status"
        sleep 5
    done
    [[ "$status" == GONE ]] || die "runtime agent $arn is still deleting: run ./setup.sh runtime delete again"
    rm -f "$ids"
    echo "deleted runtime agent $arn"
}

delete() {
    runtime_delete
    aws cloudformation delete-stack --stack-name "$stack"
    echo "deleting stack $stack..."
    aws cloudformation wait stack-delete-complete --stack-name "$stack" \
        || die "stack $stack did not delete. AgentCore can hold network interfaces in the agent subnets for hours; run ./setup.sh delete again later"
    echo "deleted stack $stack"
}

case "${1:-} ${2:-}" in
    "create "*) create ;;
    "session "*) session "${@:2}" ;;
    "delete "*) delete ;;
    "runtime create") runtime_create ;;
    "runtime session") runtime_session "${@:3}" ;;
    "runtime delete") runtime_delete ;;
    *) die "usage: ./setup.sh create | session <question> | delete | runtime create|session <question>|delete" ;;
esac
