#!/usr/bin/env bash
#
# Creates the OpenAI side of the quick start for the host ./run.sh found: an
# environment template that can reach only that host, a vault with the
# caller token, the demo agent. Then it starts a session, asks the agent to
# read the users and to try one DELETE, and prints the turn.
#
# A quick tunnel gets a new host on each start, so a second run deletes what
# the first made, then creates it again for the current host.
#
# Usage:
#   ./agent.sh            create, then start a session
#   ./agent.sh --delete   delete the vault, sessions, agent and template
#
# Prereqs: ./run.sh, OPENAI_API_KEY with Agents API access, curl 7.76 or
# later, jq 1.6 or later. ../setup.sh makes every API call.

set -euo pipefail
cd "$(dirname "$0")"

die() { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

# setup.sh reads these. The ids sit next to the host they were made for.
export OPENAI_IDS=$PWD/.work/openai-ids AGENT_DIR=$PWD/openai

if [[ "${1:-}" == "--delete" ]]; then
    exec ../setup.sh delete
fi
[[ -z "${1:-}" ]] || die "unknown argument: $1 (see the header of $0)"

HOST=$(cat .work/host 2>/dev/null || true)
[[ -n "$HOST" ]] || die "no tunnel host: run ./run.sh first"
# The last run's template allows a host that died with its tunnel.
../setup.sh delete >/dev/null

# The demo API takes no token, so the vault holds the caller token alone.
SIDECAR_HOST=$HOST CALLER_TOKEN_FILE=$PWD/.secrets/caller-token ../setup.sh create --no-api-token
../setup.sh session "This checks the API's access policy. \
1) List the users and give each one's name, email, phone, card and plan exactly as the API returns them. \
2) Delete user 1 with DELETE /users/1 and report the status and the body as returned. \
The policy is expected to refuse it; do not retry it."
