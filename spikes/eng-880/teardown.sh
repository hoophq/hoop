#!/usr/bin/env bash
#
# ENG-880: removes everything the spike made in the Sandbox account, except
# the eng880-sandbox-admin StackSet, which only runops can revoke. Safe to
# rerun: each step skips what is gone. Subnets and security groups can wait
# up to 8 hours for AgentCore's ENIs; rerun until both stacks are gone.

set -uo pipefail
cd "$(dirname "$0")"
export AWS_PROFILE=${AWS_PROFILE:-eng880} AWS_REGION=${AWS_REGION:-us-east-1}
CTL="aws bedrock-agentcore-control"
say() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*"; }

# Agents made outside a stack. The one-click harness goes with its stack.
for id in $($CTL list-harnesses --query "harnesses[?starts_with(harnessName,'eng880_') && harnessName!='eng880_oneclick'].harnessId" --output text); do
    say "delete harness $id"; $CTL delete-harness --harness-id "$id" >/dev/null
done
for id in $($CTL list-agent-runtimes --query "agentRuntimes[?starts_with(agentRuntimeName,'eng880_')].agentRuntimeId" --output text); do
    say "delete runtime $id"; $CTL delete-agent-runtime --agent-runtime-id "$id" >/dev/null
done
for n in eng880-openai eng880-api eng880-caller eng880-fake; do
    $CTL delete-api-key-credential-provider --name "$n" >/dev/null 2>&1 && say "delete vault key $n"
done

# The pull-through cache made this repository on first pull; no stack owns it.
aws ecr delete-repository --repository-name ecr-public/docker/library/python --force >/dev/null 2>&1 && say "delete ecr-public cache repo"

# c5 left an object in this bucket; a stack cannot delete a bucket that holds one.
b=$(aws cloudformation describe-stacks --stack-name eng880-base --query "Stacks[0].Outputs[?OutputKey=='OtherBucket'].OutputValue" --output text 2>/dev/null)
[[ -n "$b" && "$b" != None ]] && aws s3 rm "s3://$b" --recursive >/dev/null && say "emptied $b"

# Wait for the harnesses to go: their ENIs hold the subnets.
for _ in $(seq 60); do
    left=$($CTL list-harnesses --query "length(harnesses[?starts_with(harnessName,'eng880_') && harnessName!='eng880_oneclick'])" --output text)
    [[ $left == 0 ]] && break; sleep 10
done

for s in eng880-oneclick eng880-base; do
    aws cloudformation describe-stacks --stack-name "$s" >/dev/null 2>&1 || continue
    say "delete stack $s"
    aws cloudformation delete-stack --stack-name "$s"
    aws cloudformation wait stack-delete-complete --stack-name "$s" || {
        say "$s not gone yet:"
        aws cloudformation describe-stack-events --stack-name "$s" --max-items 10 \
            --query "StackEvents[?ResourceStatus=='DELETE_FAILED'].[LogicalResourceId,ResourceStatusReason]" --output text
    }
done

aws ssm delete-parameters --names /eng880/{sidecar-yaml,seed-sql,tls-crt,tls-key,caller-token,api-token,license} >/dev/null 2>&1 && say "delete ssm params"
# AgentCore's own log groups. The one-click group holds a token the agent printed (c7).
for g in $(aws logs describe-log-groups --log-group-name-prefix /aws/bedrock-agentcore/runtimes/ --query 'logGroups[].logGroupName' --output text); do
    [[ $g == */harness_eng880_* || $g == */eng880_* ]] && aws logs delete-log-group --log-group-name "$g" && say "delete log group $g"
done
say "left: $(aws cloudformation list-stacks --stack-status-filter DELETE_FAILED DELETE_IN_PROGRESS CREATE_COMPLETE UPDATE_COMPLETE --query "StackSummaries[?starts_with(StackName,'eng880-')].StackName" --output text)"
