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
# From here a failure stops the stack: a public tunnel to a lane that failed
# a check serves no one. The failing step prints its reason first.
trap '[[ $? -eq 0 ]] || docker compose down --remove-orphans >/dev/null 2>&1' EXIT
# A new cloudflared container, so its log holds only the new host.
docker compose up -d --wait --force-recreate --remove-orphans || {
    # Compose names only the exit code. The reason, such as a config over
    # the free tier or a refused tunnel request, is in the log of the
    # service that stopped. Neither stopped: the sidecar never got healthy.
    failed=$(docker compose ps -a --status exited --format '{{.Service}}' | head -1)
    failed=${failed:-sidecar}
    docker compose logs --no-log-prefix --tail 20 "$failed" >&2
    die "$failed did not start; its log is above"
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
# third argument sends the caller token. Behind an HTTPS proxy, -i alone
# would print the proxy's CONNECT answer before the lane's.
call() {
    curl -sS -i --suppress-connect-headers --max-time 10 -X "$1" "https://$host$2" \
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

# again <method> <path> [token]: call, retried on a transport error or a
# 5xx. For some seconds after the first 200, a new tunnel can still drop a
# lookup or answer 530 from Cloudflare's edge. The lane refuses both of
# these requests before the API, so a 5xx is never its answer. Prints the
# answer, or the last status line and fails.
again() {
    local o=""
    for _ in 1 2 3 4 5; do
        o=$(call "$@") && [[ "${o%%$'\r'*}" != HTTP/*" 5"[0-9][0-9]* ]] && { printf '%s' "$o"; return 0; }
        sleep 2
    done
    printf '%s' "${o%%$'\r'*}"
    return 1
}
out=$(again DELETE /users/1 token) \
    || die "the tunnel did not pass DELETE /users/1 to the sidecar (${out:-no answer}); run ./run.sh again"
# The tunnel speaks HTTP/2, which sends header names in lower case.
[[ "$out" == HTTP/*" 403"* ]] && grep -qi '^x-hoop-denied: policy' <<<"$out" \
    || die "DELETE /users/1 was not refused: ${out%%$'\r'*}"
c_ok "DELETE /users/1 is refused: ${out##*$'\r\n\r\n'}"

out=$(again GET /users/1) \
    || die "the tunnel did not pass a request without X-Hoop-Token to the sidecar (${out:-no answer}); run ./run.sh again"
[[ "$out" == HTTP/*" 403"* ]] || die "a request without X-Hoop-Token was not refused: ${out%%$'\r'*}"
c_ok "a request without X-Hoop-Token is refused: ${out##*$'\r\n\r\n'}"

# Only a lane that passed every check is a host for agent.sh.
echo "$host" > .work/host

cat <<EOF

The sidecar is live at https://$host. Next:

  ./agent.sh    an OpenAI session that can reach only this host
EOF
