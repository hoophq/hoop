#!/usr/bin/env bash
#
# ENG-880 AgentCore driver, throwaway: curl --aws-sigv4 against the data
# plane (c10). The AWS CLI has no invoke-harness and no
# invoke-agent-runtime-command.
#
#   ./ac.sh harness <harness-arn> <session-id> <prompt>     InvokeHarness
#   ./ac.sh cmd     <arn>         <session-id> <script> [args]  InvokeAgentRuntimeCommand
#   ./ac.sh runtime <runtime-arn> <session-id> <prompt>     InvokeAgentRuntime
#   ./ac.sh session                                          a new 39-char session id
#
# cmd takes the harness ARN for a harness: its runtime refuses direct calls.
# Output: the decoded event stream as JSON lines (evstream.py).

set -euo pipefail
cd "$(dirname "$0")"
export AWS_PROFILE=${AWS_PROFILE:-eng880} AWS_REGION=${AWS_REGION:-us-east-1}
HOST="https://bedrock-agentcore.$AWS_REGION.amazonaws.com"
# macOS curl 8.7.1 signs an encoded ARN in the path wrong (403 signature
# mismatch); 8.17.0 signs it right.
CURL=${CURL:-docker run --rm -i curlimages/curl:8.17.0}

enc() { jq -rn --arg v "$1" '$v|@uri'; }

# The keys go to curl in a config on stdin: argv shows in the process list.
call() { # call <path> <session-id> <json-body> [accept]
    eval "$(aws configure export-credentials --format env)"
    printf 'user = "%s:%s"\nheader = "x-amz-security-token: %s"\n' \
        "$AWS_ACCESS_KEY_ID" "$AWS_SECRET_ACCESS_KEY" "${AWS_SESSION_TOKEN:-}" |
        $CURL -sS -m "${CURL_MAX:-300}" -K - --aws-sigv4 "aws:amz:$AWS_REGION:bedrock-agentcore" \
            -H "X-Amzn-Bedrock-AgentCore-Runtime-Session-Id: $2" \
            -H 'Content-Type: application/json' -H "Accept: ${4:-application/json}" \
            -w '%{stderr}{"event":"http","status":%{http_code},"content_type":"%{content_type}","seconds":%{time_total}}\n' \
            --data-binary "$3" "$HOST$1" | python3 evstream.py
}

case "${1:-}" in
    session) printf 'eng880-%s\n' "$(openssl rand -hex 16)" ;;
    harness)
        call "/harnesses/invoke?harnessArn=$(enc "$2")" "$3" \
            "$(jq -cn --arg p "$4" '{messages: [{role: "user", content: [{text: $p}]}]}')" ;;
    cmd)
        # A harness runs the command in bash. A Runtime agent splits it like
        # argv and runs the first word with no shell, so pipes and newlines
        # are lost. bash -c over base64 works on both. In the script $1 is the
        # session id, then the args (no spaces, no single quotes).
        c="bash -c 'echo $(printf '%s' "$4" | base64 | tr -d '\n') | base64 -d | bash -s -- $3 ${*:5}'"
        call "/runtimes/$(enc "$2")/commands" "$3" \
            "$(jq -cn --arg c "$c" '{command: $c, timeout: 120}')" application/vnd.amazon.eventstream ;;
    runtime)
        call "/runtimes/$(enc "$2")/invocations" "$3" "$(jq -cn --arg p "$4" '{prompt: $p}')" ;;
    *) echo "usage: see the header of $0" >&2; exit 2 ;;
esac
