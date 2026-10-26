#!/usr/bin/env bash
#
# ENG-880: creates and deletes the agents on top of the eng880-base stack.
# Throwaway. Variants through environment variables:
#
#   IMAGE=agent|base      custom image (eng880:agent) or the harness base
#   MODEL=bedrock|litellm Bedrock Haiku, or OpenAI through the sidecar (c9)
#   S3SVC=|false|true     networkModeConfig.requireServiceS3Endpoint (c5); unset on create
#   MEMORY=disabled|managed
#   NETWORK=VPC|PUBLIC
#   TOOLS=|mcp            mcp: a remote_mcp tool on the probe lane whose
#                         headers the token vault fills (c7)
#   ENVTOKENS=1|0         0: no token in environmentVariables (c7 vault-only)
#   APIBASE=<url>         litellm apiBase (default: the sidecar's openai lane)
#
#   ./agents.sh harness <name>     create a harness, print its ARN
#   ./agents.sh runtime <name>     create a Runtime agent from eng880:agent
#   ./agents.sh get <name>         status of a harness or a runtime
#   ./agents.sh delete <name>      delete a harness or a runtime by name
#
# Ids go to .work/<name>.arn.

set -euo pipefail
cd "$(dirname "$0")"
export AWS_PROFILE=${AWS_PROFILE:-eng880} AWS_REGION=${AWS_REGION:-us-east-1}
CTL="aws bedrock-agentcore-control"
mkdir -p .work

out() { aws cloudformation describe-stacks --stack-name eng880-base --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue" --output text; }

network() {
    jq -cn --arg mode "${NETWORK:-VPC}" --arg sg "$(out AgentSg)" --arg subnets "$(out AgentSubnets)" \
        --arg s3 "${S3SVC:-}" \
        'if $mode == "PUBLIC" then {networkMode: "PUBLIC"} else
         {networkMode: "VPC", networkModeConfig: ({securityGroups: [$sg], subnets: ($subnets | split(","))}
          # CreateHarness refuses this field; only UpdateHarness sets it.
          + if $s3 == "" then {} else {requireServiceS3Endpoint: ($s3 == "true")} end)} end'
}

harness_body() {
    local model
    case "${MODEL:-bedrock}" in
        bedrock) model='{"bedrockModelConfig": {"modelId": "us.anthropic.claude-haiku-5-5"}}' ;;
        litellm) model=$(jq -cn --arg key "$(cat .work/openai-key.arn)" \
            --arg base "${APIBASE:-http://sidecar.eng880.internal:8445/v1}" \
            '{liteLlmModelConfig: {modelId: "openai/gpt-5.4-mini", apiKeyArn: $key, apiBase: $base}}') ;;
    esac
    local tools='[]'
    if [[ ${TOOLS:-} == mcp ]]; then
        tools=$(jq -cn --arg caller "$(cat .work/caller-key.arn)" --arg api "$(cat .work/api-key.arn)" \
            '[{type: "remote_mcp", name: "probe", config: {remoteMcp: {
               url: "https://sidecar.eng880.internal:8444/mcp",
               headers: {"X-Hoop-Token": "${\($caller)}", "x-api-key": "${\($api)}"}}}}]')
    fi
    jq -n --arg name "$1" --arg role "$(out AgentRoleArn)" --argjson net "$(network)" \
        --arg image "$(out RepoUri):agent" --arg img "${IMAGE:-agent}" --argjson model "$model" \
        --arg mem "${MEMORY:-disabled}" --argjson tools "$tools" --arg envtok "${ENVTOKENS:-1}" \
        --arg caller "$(cat .secrets/caller-token)" --arg api "$(cat .secrets/api-token)" \
        '{harnessName: $name, executionRoleArn: $role,
          environment: {agentCoreRuntimeEnvironment: {networkConfiguration: $net,
            lifecycleConfiguration: {idleRuntimeSessionTimeout: 900, maxLifetime: 3600}}},
          model: $model,
          systemPrompt: [{text: "You are a test agent. Use the shell tool to run what the user asks, then report each command and its output verbatim."}],
          environmentVariables: ({SIDECAR_HOST: "sidecar.eng880.internal"}
            + if $envtok == "1" then {HOOP_CALLER_TOKEN: $caller, CUSTOMER_API_TOKEN: $api} else {} end
            # Python clients read certifi, not the system store that holds the sidecar CA.
            + if $img == "agent" then {SSL_CERT_FILE: "/etc/ssl/certs/ca-certificates.crt"} else {} end),
          maxIterations: 10, timeoutSeconds: 600}
         + (if $tools != [] then {tools: $tools} else {} end)
         + (if $img == "agent" then {environmentArtifact: {containerConfiguration: {containerUri: $image}}} else {} end)
         + (if $mem == "disabled" then {memory: {disabled: {}}} else {} end)'
}

case "${1:-}" in
    harness)
        harness_body "$2" > ".work/$2.request.json"
        $CTL create-harness --cli-input-json "file://.work/$2.request.json" > ".work/$2.json"
        jq -r '.harness.arn // .harnessArn // .arn' ".work/$2.json" | tee ".work/$2.arn" ;;
    runtime)
        $CTL create-agent-runtime --agent-runtime-name "$2" --role-arn "$(out AgentRoleArn)" \
            --agent-runtime-artifact "containerConfiguration={containerUri=$(out RepoUri):agent}" \
            --network-configuration "$(network)" \
            --environment-variables "MODEL_ID=us.anthropic.claude-haiku-5-5,SIDECAR_HOST=sidecar.eng880.internal,HOOP_CALLER_TOKEN=$(cat .secrets/caller-token)" \
            > ".work/$2.json"
        jq -r '.agentRuntimeArn' ".work/$2.json" | tee ".work/$2.arn" ;;
    get)
        arn=$(cat ".work/$2.arn")
        if [[ $arn == *:harness/* ]]; then $CTL get-harness --harness-id "${arn##*/}"
        else $CTL get-agent-runtime --agent-runtime-id "${arn##*/}"; fi ;;
    delete)
        arn=$(cat ".work/$2.arn")
        if [[ $arn == *:harness/* ]]; then $CTL delete-harness --harness-id "${arn##*/}"
        else $CTL delete-agent-runtime --agent-runtime-id "${arn##*/}"; fi
        rm -f ".work/$2.arn" ;;
    *) echo "usage: see the header of $0" >&2; exit 2 ;;
esac
