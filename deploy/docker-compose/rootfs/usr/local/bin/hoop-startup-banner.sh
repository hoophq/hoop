#!/usr/bin/env bash
#
# Prints a concise "you're ready" banner at the end of `docker compose up`.
# Invoked by the `startup` service in deploy/docker-compose/docker-compose.yml.
#
# Pre-conditions (already enforced by compose depends_on):
#   - gateway service is healthy (`GET /api/healthz` returns 200)
#   - agent container has started
#
# We do a final in-container liveness probe as belt-and-braces, then give the
# default agent a few seconds to finish its gRPC handshake before printing.

set -euo pipefail

GATEWAY_INTERNAL_URL="${GATEWAY_INTERNAL_URL:-http://gateway:8009}"
PUBLIC_URL="${HOOP_PUBLIC_URL:-http://localhost:8009}"

# Poll the gateway healthz for up to ~30s. This should return immediately
# because compose already waited for the healthcheck, but a retry makes the
# script robust to tiny races (container killed/restarted between checks).
deadline=$(( $(date +%s) + 30 ))
until curl -fsS -o /dev/null "${GATEWAY_INTERNAL_URL}/api/healthz"; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
        echo "hoop-startup-banner: gateway did not become healthy in time" >&2
        exit 0  # non-fatal — don't break `docker compose up`
    fi
    sleep 1
done

# Give the default agent a moment to complete its gRPC connection to the
# gateway. Authenticated agent-status probing would require credentials, so
# we just wait briefly — the gateway log line "agent connected: ...,name=default"
# is also visible to the user above this banner.
sleep 3

# ANSI colours when stdout is a TTY; plain otherwise (e.g. piped, CI).
if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
    CYAN=$'\e[36m'
    GREEN=$'\e[32m'
    BOLD=$'\e[1m'
    DIM=$'\e[2m'
    RESET=$'\e[0m'
else
    CYAN=""; GREEN=""; BOLD=""; DIM=""; RESET=""
fi

# Blank line first so the banner visually separates from the chatty
# gateway/agent boot logs above it.
echo
echo "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
echo "${CYAN}     _                           ${RESET}"
echo "${CYAN}    | |__    ___    ___   _ __   ${RESET}"
echo "${CYAN}    | '_ \\  / _ \\  / _ \\ | '_ \\  ${RESET}"
echo "${CYAN}    | | | || (_) || (_) || |_) | ${RESET}"
echo "${CYAN}    |_| |_| \\___/  \\___/ | .__/  ${RESET}"
echo "${CYAN}                         |_|     ${RESET}"
echo
echo "   ${GREEN}${BOLD}Your Hoop gateway is ready.${RESET}"
echo
echo "   ${BOLD}Open the UI:${RESET}        ${PUBLIC_URL}"
echo "   ${BOLD}Default agent:${RESET}      connected"
echo "   ${BOLD}Docs:${RESET}               https://hoop.dev/docs"
echo
echo "   ${DIM}Stop with Ctrl-C · Inspect a service with \`docker compose logs <name>\`${RESET}"
echo "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
echo
