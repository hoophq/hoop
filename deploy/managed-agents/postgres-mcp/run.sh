#!/usr/bin/env bash
#
# Brings up the stack and writes .env: the MCP token (kept across runs),
# MCP_HOST, and the compose files and profiles of the mode, so a bare
# `docker compose` in this directory acts on the same stack.
#
# Usage:
#   MCP_HOST=mcp.example.com ./run.sh   production edge: Linux, DNS, ports 80/443
#   ./run.sh --test                     local, no public URL; then ./demo.sh
#   ./run.sh --tunnel                   Cloudflare quick tunnel; then ./e2e.sh
#   ./run.sh down                       remove the stack, keep .env and the certificate

set -euo pipefail
cd "$(dirname "$0")"

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

ALL_FILES=docker-compose.yml:docker-compose.test.yml

if [[ "${1:-}" == "down" ]]; then
    # Every file and profile, so a stack from any mode comes down. `rm -v`
    # drops the anonymous volumes (the demo database) and keeps caddy-data:
    # a new certificate on each cycle runs into Let's Encrypt's rate limits.
    export DBHUB_AUTH_TOKEN=x MCP_HOST=x COMPOSE_FILE=$ALL_FILES COMPOSE_PROFILES=tunnel
    docker compose rm -sfv
    docker compose down --remove-orphans
    exit 0
fi

case "${1:-}" in
    "")       MODE="prod";   FILES=docker-compose.yml; PROFILES="" ;;
    --test)   MODE="test";   FILES=$ALL_FILES;         PROFILES="" ;;
    --tunnel) MODE="tunnel"; FILES=$ALL_FILES;         PROFILES="tunnel" ;;
    *)        die "unknown argument: $1 (see the header of $0)" ;;
esac

command -v docker >/dev/null || die "missing required tool: docker"
command -v openssl >/dev/null || die "missing required tool: openssl"

# !reset and !override in the test overlay need Compose 2.24.
v=$(docker compose version --short); v=${v#v}
[[ "$(printf '%s\n' 2.24.0 "$v" | sort -V | head -1)" == 2.24.0 ]] \
    || die "docker compose $v is older than 2.24.0"

if [[ "$MODE" == prod ]]; then
    [[ -n "${MCP_HOST:-}" ]] || die "set MCP_HOST to the public DNS name of this host"
    # Docker Desktop rewrites the source address of published ports, so every
    # caller would look like the VM and get 403.
    [[ "$(uname -s)" == Linux ]] \
        || die "the production edge needs a Linux host; use --test or --tunnel here"
else
    MCP_HOST=mcp.invalid # replaced below in tunnel mode; unused in test mode
fi

# The token outlives `down`: the vault credential holds the same value.
DBHUB_AUTH_TOKEN=$(sed -n 's/^DBHUB_AUTH_TOKEN=//p' .env 2>/dev/null || true)
[[ -n "$DBHUB_AUTH_TOKEN" ]] || DBHUB_AUTH_TOKEN=$(openssl rand -hex 32)
export DBHUB_AUTH_TOKEN MCP_HOST COMPOSE_FILE=$FILES COMPOSE_PROFILES=$PROFILES

write_env() {
    # umask: a new file is created 0600, not readable for a moment before chmod.
    (umask 077; cat > .env) <<EOF
# Written by ./run.sh. Holds the MCP token: keep it out of git.
DBHUB_AUTH_TOKEN=$DBHUB_AUTH_TOKEN
MCP_HOST=$MCP_HOST
COMPOSE_FILE=$FILES
COMPOSE_PROFILES=$PROFILES
EOF
    chmod 600 .env # a file from an older run keeps its mode otherwise
}
# Before `up`: if it fails, a bare `docker compose` still names this stack.
write_env

c_step "Starting postgres, sidecar, dbhub, caddy ($MODE)"
# A tunnel from an earlier --tunnel run would stay up: its profile is only
# inactive now. --remove-orphans covers the move to production.
if [[ "$MODE" == test ]]; then docker compose rm -sf cloudflared >/dev/null 2>&1 || true; fi
docker compose up -d --wait --remove-orphans
c_ok "all services healthy"

if [[ "$MODE" == tunnel ]]; then
    c_step "Waiting for the quick tunnel"
    # Not MCP_HOST itself: compose refuses to run while that is empty.
    host=""
    for _ in $(seq 60); do
        host=$(docker compose logs cloudflared 2>/dev/null \
            | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | tail -1 | sed 's|https://||') || true
        [[ -n "$host" ]] && break
        sleep 1
    done
    [[ -n "$host" ]] || die "no trycloudflare.com URL in the cloudflared log after 60 s"
    MCP_HOST=$host
    write_env
fi

case "$MODE" in
prod)   url="https://$MCP_HOST/mcp"; next="register the agent: README.md, steps 2 to 4" ;;
test)   url="none in test mode";      next="./demo.sh" ;;
tunnel) url="https://$MCP_HOST/mcp"; next="./e2e.sh (needs ANTHROPIC_API_KEY)" ;;
esac

cat <<EOF

ready ($MODE)

  MCP endpoint   $url
  Token          DBHUB_AUTH_TOKEN in .env
  Next           $next

  Audit trail, one JSON line per statement:
      docker compose logs --no-log-prefix sidecar | grep '^{'

  Teardown:      ./run.sh down
EOF
