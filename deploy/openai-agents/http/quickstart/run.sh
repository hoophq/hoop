#!/usr/bin/env bash
#
# Starts the quick start (the sidecar with hoop's demo API inside it, and a
# Cloudflare quick tunnel), then proves the lane through the tunnel's public
# host: a masked response, a refused DELETE, a refused caller.
#
# Usage:
#   ./run.sh        start; a second run restarts with a new tunnel host
#   ./run.sh down   stop the stack; keep the caller token

set -euo pipefail
cd "$(dirname "$0")"

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

if [[ "${1:-}" == "down" ]]; then
    docker compose down --remove-orphans
    rm -f .work/host
    exit 0
fi
[[ -z "${1:-}" ]] || die "unknown argument: $1 (see the header of $0)"

for t in docker openssl curl; do command -v "$t" >/dev/null || die "missing required tool: $t"; done

# The token outlives `down`: the vault credential holds the same value. The
# sidecar runs as another user and must read the file, so the file is 0644
# and its 0700 folder keeps other users of this machine out.
mkdir -p .secrets .work && chmod 700 .secrets
[[ -s .secrets/caller-token ]] || (umask 077; openssl rand -hex 24 > .secrets/caller-token)
chmod 644 .secrets/caller-token
# curl reads the header from this file: an argument shows in the process list.
(umask 077; printf 'X-Hoop-Token: %s\n' "$(cat .secrets/caller-token)" > .secrets/token-header)

c_step "Starting the sidecar and the tunnel"
# The old host dies with its tunnel: a failed start must leave none behind
# for agent.sh.
rm -f .work/host
# A new cloudflared container, so its log holds only the new host.
docker compose up -d --wait --force-recreate --remove-orphans || {
    # Compose names only the exit code. The reason, such as a config over
    # the free tier, is in the sidecar's log.
    docker compose logs --no-log-prefix --tail 20 sidecar >&2
    die "the sidecar did not start; its log is above"
}
c_ok "sidecar healthy"

c_step "Waiting for the tunnel host"
host=""
for _ in $(seq 60); do
    # Not api.trycloudflare.com: a failed tunnel request names that host.
    host=$(docker compose logs cloudflared 2>/dev/null \
        | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | grep -v '^https://api\.' \
        | tail -1 | sed 's|https://||') || true
    [[ -n "$host" ]] && break
    sleep 1
done
[[ -n "$host" ]] || die "no trycloudflare.com host in the cloudflared log after 60 s"
c_ok "https://$host"

c_step "Checking the lane through https://$host"
# call <method> <path> [token]: prints the status line, then the body. Any
# third argument sends the caller token.
call() {
    curl -sS -i --max-time 10 -X "$1" "https://$host$2" \
        ${3:+-H @.secrets/token-header} -H "X-OpenAI-Session-Id: run.sh"
}
# A new quick tunnel takes some seconds to resolve and connect. A resolver
# that asked too early keeps the miss for the zone's 60 s negative TTL, so
# wait past it.
out=""
for _ in $(seq 60); do
    out=$(call GET /users/1 token 2>/dev/null) && [[ "$out" == HTTP/*" 200"* ]] && break
    sleep 2
done
[[ "$out" == HTTP/*" 200"* ]] || die "GET /users/1 did not answer 200 through the tunnel after 2 minutes: ${out%%$'\r'*}"
body=${out#*$'\r\n\r\n'}
for marker in EMAIL_ADDRESS PHONE_NUMBER CREDIT_CARD; do
    [[ "$body" == *"[REDACTED:$marker]"* ]] || die "GET /users/1 holds no [REDACTED:$marker]: $body"
done
[[ "$body" != *"@example"* ]] || die "GET /users/1 leaked an email address: $body"
c_ok "GET /users/1 is masked:"
printf '%s\n' "$body" | sed 's/^/        /'

# again <command>: retries a transport error, not an HTTP answer. A
# resolver can drop one lookup of a new host.
again() {
    local o
    for _ in 1 2 3; do o=$("$@") && { printf '%s' "$o"; return 0; }; sleep 2; done
    return 1
}
out=$(again call DELETE /users/1 token) || die "DELETE /users/1 got no answer through the tunnel"
# The tunnel speaks HTTP/2, which sends header names in lower case.
[[ "$out" == HTTP/*" 403"* ]] && grep -qi '^x-hoop-denied: policy' <<<"$out" \
    || die "DELETE /users/1 was not refused: ${out%%$'\r'*}"
c_ok "DELETE /users/1 is refused: ${out##*$'\r\n\r\n'}"

out=$(again call GET /users/1) || die "a request without X-Hoop-Token got no answer through the tunnel"
[[ "$out" == HTTP/*" 403"* ]] || die "a request without X-Hoop-Token was not refused: ${out%%$'\r'*}"
c_ok "a request without X-Hoop-Token is refused: ${out##*$'\r\n\r\n'}"

# Only a lane that passed every check is a host for agent.sh.
echo "$host" > .work/host

cat <<EOF

The sidecar is live at https://$host. Next:

  ./agent.sh    an OpenAI session that can reach only this host
EOF
