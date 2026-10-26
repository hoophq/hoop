#!/usr/bin/env bash
#
# Brings up the stack and writes .env: the MCP token (kept across runs),
# MCP_HOST, and the compose files and profiles of the mode, so a bare
# `docker compose` in this directory acts on the same stack. Each start
# fetches OpenAI's IP list into ranges/openai.caddy, the only callers Caddy
# admits.
#
# Usage:
#   MCP_HOST=mcp.example.com ./run.sh   production edge: Linux, DNS, ports 80/443
#   ./run.sh --test                     local, no public URL; then ./demo.sh
#   ./run.sh --tunnel                   Cloudflare quick tunnel; then ./e2e.sh
#   ./run.sh ranges                     fetch the IP list again and reload Caddy
#   ./run.sh down                       remove the stack, keep .env and the certificate

set -euo pipefail
cd "$(dirname "$0")"

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

ALL_FILES=docker-compose.yml:docker-compose.test.yml
# OpenAI's list for ChatGPT connectors. The Agents API docs name no list for
# MCP calls; the calls of four sessions (ENG-860, ENG-866) were all in this
# one. chatgpt-agents.json is sandbox egress, where any customer's code runs.
RANGE_LIST=https://openai.com/chatgpt-connectors.json

# valid_prefix <prefix>: one Caddy can load, and no wider than /8 or an IPv6
# /16. OpenAI's widest is a /16; a wider one would admit a large part of the
# internet.
valid_prefix() {
    local ip=${1%/*} len=${1##*/} o n='(0|[1-9][0-9]{0,2})'
    # No leading zeros: Go, and so Caddy, refuses them.
    [[ "$1" == */* && "$len" =~ ^$n$ ]] || return 1
    if [[ "$ip" =~ ^$n\.$n\.$n\.$n$ ]]; then
        for o in ${ip//./ }; do (( o <= 255 )) || return 1; done
        (( len >= 8 && len <= 32 ))
    else
        [[ "$ip" == *:* && "$ip" =~ ^[0-9a-fA-F:]+$ ]] && (( len >= 16 && len <= 128 ))
    fi
}

# write_ranges: ranges/openai.caddy, or fail and leave the old file. The old
# one stays as ranges/.openai.caddy.prev for reload_caddy. Caddy mounts the
# directory, not the file: a file mount keeps the inode that mv replaces, and
# Caddy would go on reading the old list.
write_ranges() {
    local body list p tmp
    body=$(curl -fsS --max-time 30 "$RANGE_LIST") || die "could not fetch $RANGE_LIST"
    # jq exits 0 on an empty array: the length check refuses that too.
    jq -e '.prefixes | length > 0' >/dev/null 2>&1 <<<"$body" || die "$RANGE_LIST holds no prefixes"
    list=$(jq -r '.prefixes[] | .ipv4Prefix // .ipv6Prefix // "missing"' <<<"$body" | sort -u)
    for p in $list; do
        valid_prefix "$p" || die "$RANGE_LIST holds '$p': not a prefix, or wider than /8"
    done
    mkdir -p ranges
    if [[ -f ranges/openai.caddy ]]; then cp -p ranges/openai.caddy ranges/.openai.caddy.prev; fi
    tmp=$(mktemp ranges/.openai.caddy.XXXXXX)
    {
        printf '# Written by ./run.sh at %s from %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$RANGE_LIST"
        printf '@outside not client_ip %s\n' "$(tr '\n' ' ' <<<"$list")"
    } > "$tmp"
    chmod 644 "$tmp" # mktemp makes it 0600, and Caddy runs as another user
    mv "$tmp" ranges/openai.caddy
    c_ok "$(wc -l <<<"$list" | tr -d ' ') OpenAI prefixes in ranges/openai.caddy"
}

# reload_caddy: a running Caddy keeps the list it loaded. A failed reload
# keeps the running config, and the old list goes back on disk, so the next
# Caddy start does not fail on the refused one.
reload_caddy() {
    local out
    out=$(docker compose exec -T caddy caddy reload --config /etc/caddy/Caddyfile 2>&1) && return
    if [[ -f ranges/.openai.caddy.prev ]]; then mv ranges/.openai.caddy.prev ranges/openai.caddy; fi
    die "caddy refused the config and runs the old list: $out"
}

if [[ "${1:-}" == "down" ]]; then
    # Every file and profile, so a stack from any mode comes down. `rm -v`
    # drops the anonymous volumes (the demo database) and keeps caddy-data:
    # a new certificate on each cycle runs into Let's Encrypt's rate limits.
    export DBHUB_AUTH_TOKEN=x MCP_HOST=x COMPOSE_FILE=$ALL_FILES COMPOSE_PROFILES=tunnel
    docker compose rm -sfv
    docker compose down --remove-orphans
    exit 0
fi

command -v docker >/dev/null || die "missing required tool: docker"
command -v openssl >/dev/null || die "missing required tool: openssl"
command -v curl >/dev/null || die "missing required tool: curl"
command -v jq >/dev/null || die "missing required tool: jq"

if [[ "${1:-}" == "ranges" ]]; then
    # .env names the compose files, so `docker compose` finds the running stack.
    [[ -s .env ]] || die "no .env: start the stack with ./run.sh first"
    c_step "Refreshing OpenAI's IP list"
    write_ranges
    reload_caddy
    c_ok "Caddy reloaded"
    exit 0
fi

case "${1:-}" in
    "")       MODE="prod";   FILES=docker-compose.yml; PROFILES="" ;;
    --test)   MODE="test";   FILES=$ALL_FILES;         PROFILES="" ;;
    --tunnel) MODE="tunnel"; FILES=$ALL_FILES;         PROFILES="tunnel" ;;
    *)        die "unknown argument: $1 (see the header of $0)" ;;
esac

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

c_step "Fetching OpenAI's IP list"
write_ranges

c_step "Starting postgres, sidecar, dbhub, caddy ($MODE)"
# A tunnel from an earlier --tunnel run would stay up: its profile is only
# inactive now. --remove-orphans covers the move to production.
if [[ "$MODE" == test ]]; then docker compose rm -sf cloudflared >/dev/null 2>&1 || true; fi
docker compose up -d --wait --remove-orphans
# `up` leaves a running Caddy as it is.
reload_caddy
c_ok "all services healthy"

if [[ "$MODE" == prod ]]; then
    c_step "Checking https://$MCP_HOST/mcp"
    # `up --wait` cannot see the certificate. Caddy's 403 for this caller,
    # over a verified TLS connection, proves the certificate and the route.
    code=""
    for _ in $(seq 90); do
        code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -X POST \
            --resolve "$MCP_HOST:443:127.0.0.1" "https://$MCP_HOST/mcp") || true
        [[ "$code" == 403 ]] && break
        sleep 1
    done
    [[ "$code" == 403 ]] || die "https://$MCP_HOST/mcp gave '${code:-nothing}', not Caddy's 403: check DNS, ports 80 and 443, and docker compose logs caddy"
    c_ok "certificate issued, route answers"
fi

if [[ "$MODE" == tunnel ]]; then
    c_step "Waiting for the quick tunnel"
    # A new container, so the log holds only the new URL: a restarted one
    # keeps its old log, and the URL in it is dead.
    docker compose up -d --force-recreate --no-deps cloudflared >/dev/null 2>&1 \
        || die "could not recreate cloudflared"
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
prod)   url="https://$MCP_HOST/mcp"; next="create the OpenAI side: README.md, steps 2 and 3" ;;
test)   url="none in test mode";      next="./demo.sh" ;;
tunnel) url="https://$MCP_HOST/mcp"; next="./e2e.sh (needs OPENAI_API_KEY)" ;;
esac

cat <<EOF

ready ($MODE)

  MCP endpoint   $url
  Token          DBHUB_AUTH_TOKEN in .env
  Next           $next

  Audit trail, one JSON line per statement:
      docker compose logs --no-log-prefix sidecar | grep '^{'

  OpenAI's list changes: run ./run.sh ranges every hour (README.md).
  Teardown:      ./run.sh down
EOF
