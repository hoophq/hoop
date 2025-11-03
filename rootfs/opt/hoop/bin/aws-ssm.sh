#!/bin/bash

set -eo pipefail

[[ "$CONNECTION_DEBUG" == "1" ]] && set -x

if [[ "$HOOP_CLIENT_VERB" == "connect" ]]; then
    if [[ -z "$INSTANCE_ID" ]]; then
      echo "Error: INSTANCE_ID is required for connect"
      exit 1
    fi
    aws ssm start-session --target $INSTANCE_ID
    exit $?
fi

PIPE_EXEC=${PIPE_EXEC:=/bin/bash}

STDIN_INPUT=$(< /dev/stdin)
if [[ -z "$STDIN_INPUT" ]]; then
  echo "Error: missing input to ssm-exec"
  exit 1
fi



INSTANCE_ID=""
while IFS= read -r line; do
  if [[ $line =~ instance-id=([^[:space:]*/]+) ]]; then
    INSTANCE_ID="${BASH_REMATCH[1]}"
    break
  fi
done <<< "$STDIN_INPUT"

if [[ -z "$INSTANCE_ID" ]]; then
  echo -e "
    Error: EC2 instance-id not found in input
    Write the EC2 instance-id as '# instance-id=<your-instance-id>' in the input
    Make sure to use the appropriate comment syntax depending on your exec configuration ($PIPE_EXEC).

    Examples (bash|python):
    # instance-id=i-0d1a222959d48ec0c

    <script-content>

    Example (node):
    // instance-id=i-0d1a222959d48ec0c

    <script-content>"
  exit 1
fi

STDIN_INPUT_ENC="$(base64 -w0 <<< $STDIN_INPUT)"
COMMAND_ID=$(aws ssm send-command \
    --document-name "AWS-RunShellScript" \
    --instance-ids "$INSTANCE_ID" \
    --parameters "{\"commands\":[\"echo $STDIN_INPUT_ENC | base64 --decode | $PIPE_EXEC \"]}" \
    --query "Command.CommandId" \
    --output text)

while true; do
    STATUS=$(aws ssm list-command-invocations --command-id "$COMMAND_ID" --instance-id "$INSTANCE_ID" --query "CommandInvocations[0].Status" --output text)
    if [[ "$STATUS" == "Success" ]] || [[ "$STATUS" == "Failed" ]] || [[ "$STATUS" == "Cancelled" ]]; then
        break
    fi
    sleep 5
done

# Get the output
RESULT=$(aws ssm get-command-invocation \
    --command-id "$COMMAND_ID" \
    --instance-id "$INSTANCE_ID" \
    --output json)

# Extract and print to appropriate streams
STDOUT=$(echo "$RESULT" | jq -r '.StandardOutputContent')
STDERR=$(echo "$RESULT" | jq -r '.StandardErrorContent')
EXIT_CODE=$(echo "$RESULT" | jq -r '.ResponseCode')

echo "$STDOUT"
if [[ -n "$STDERR" ]]; then
  echo "$STDERR" >&2
fi

exit $EXIT_CODE