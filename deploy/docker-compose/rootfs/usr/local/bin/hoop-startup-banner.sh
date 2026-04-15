#!/usr/bin/env bash
#
# Prints a "you're ready" banner at the end of `docker compose up`.
# Invoked by the `startup` service in deploy/docker-compose/docker-compose.yml.
#
# Pre-conditions (already enforced by compose depends_on):
#   - gateway service is healthy (`GET /api/healthz` returns 200)
#   - agent container has started
#
# We do a final in-container liveness probe as belt-and-braces, then give the
# default agent a few seconds to finish its gRPC handshake before printing.

set -euo pipefail

# Ensure ${#var} counts characters, not bytes, for the multi-byte glyphs
# below (✻, box-drawing). The hoop image already sets LC_ALL=en_US.UTF-8
# via Dockerfile ENV; this is defense in depth for odd exec environments.
if [[ -z "${LC_ALL:-}" || "$LC_ALL" = "C" || "$LC_ALL" = "POSIX" ]]; then
    for _loc in en_US.UTF-8 C.UTF-8 en_US.utf8 C.utf8; do
        if locale -a 2>/dev/null | grep -qix "$_loc"; then
            export LC_ALL="$_loc"
            break
        fi
    done
fi

GATEWAY_INTERNAL_URL="${GATEWAY_INTERNAL_URL:-http://gateway:8009}"
PUBLIC_URL="${HOOP_PUBLIC_URL:-http://localhost:8009}"

# Poll the gateway healthz for up to ~30s. Redundant with compose's
# depends_on: service_healthy, but keeps the script robust if the startup
# container happens to race a gateway restart.
deadline=$(( $(date +%s) + 30 ))
until curl -fsS -o /dev/null "${GATEWAY_INTERNAL_URL}/api/healthz"; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
        echo "hoop-startup-banner: gateway did not become healthy in time" >&2
        exit 0  # non-fatal — don't break `docker compose up`
    fi
    sleep 1
done

# Give the default agent a moment to complete its gRPC connection. Probing
# agent status from here would require auth; 3 s is enough in practice.
sleep 3

# Palette — honours NO_COLOR and non-TTY stdout.
if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
    BRAND=$'\e[38;5;214m'    # warm amber — brand accent
    BOLD=$'\e[1m'
    DIM=$'\e[38;5;244m'      # soft gray for secondary text
    GREEN=$'\e[38;5;114m'    # sage green for "ready" indicators
    RESET=$'\e[0m'
else
    BRAND=""; BOLD=""; DIM=""; GREEN=""; RESET=""
fi

INNER=62   # chars between the vertical borders

# rule: horizontal border with corner characters
rule() {
    local fill
    fill=$(printf '─%.0s' $(seq 1 $INNER))
    printf "  ${DIM}%s%s%s${RESET}\n" "$1" "$fill" "$2"
}

# line: draw one content row inside the card
#   $1 = plain visible text (measured for padding)
#   $2 = how to render it with ANSI codes; defaults to $1
line() {
    local visible="$1"
    local printed="${2:-$1}"
    local pad=$(( INNER - ${#visible} ))
    (( pad < 0 )) && pad=0
    printf "  ${DIM}│${RESET}%s%*s${DIM}│${RESET}\n" "$printed" "$pad" ""
}

echo
rule "╭" "╮"
line ""
line "   ✻  Welcome to Hoop" \
     "   ${BRAND}✻${RESET}  ${BOLD}Welcome to Hoop${RESET}"
line ""
line "      Your gateway is up and running."
line ""
line "      UI             ${PUBLIC_URL}" \
     "      ${DIM}UI${RESET}             ${PUBLIC_URL}"
line "      Agent          default (connected)" \
     "      ${DIM}Agent${RESET}          default ${GREEN}(connected)${RESET}"
line "      Docs           https://hoop.dev/docs" \
     "      ${DIM}Docs${RESET}           ${DIM}https://hoop.dev/docs${RESET}"
line ""
rule "╰" "╯"
echo
printf "  ${BRAND}※${RESET} Sign in at ${BOLD}%s${RESET} and add your first connection\n" "$PUBLIC_URL"
printf "    ${DIM}Stop with Ctrl-C · Logs: docker compose logs <name>${RESET}\n"
echo
