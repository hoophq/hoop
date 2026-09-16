#!/usr/bin/env bash
#
# Brings up the three-topology SSH stack:
#
#   1. mint a CA, two host keys and ONE user certificate
#   2. build the hoop-inspect image from the local sidecar tree
#   3. compose up
#
# One certificate opens all three paths, which is the thing to notice. The
# bastions do not issue anything and do not re-sign anything; they carry a
# connection whose identity was decided by the CA and is verified at the end.
#
# Usage:
#   ./run.sh              bring everything up. The image is rebuilt whenever
#                         ../../../sidecar or ../../../libhoop changed, so a
#                         green ./demo.sh is a result about the current code
#   ./run.sh --rebuild    rebuild even when neither tree changed
#   ./run.sh down         tear down, including the generated keys

set -euo pipefail
cd "$(dirname "$0")"

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

if [[ "${1:-}" == "down" ]]; then
    # --profile ai so the analyzer container is removed too; compose ignores
    # a profile whose services never started.
    docker compose --profile ai down -v --remove-orphans
    rm -rf keys
    exit 0
fi

REBUILD=""
[[ "${1:-}" == "--rebuild" ]] && REBUILD=1

need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }
need docker; need ssh-keygen

# sha256 of stdin or of the named files. macOS ships shasum, most Linux
# images ship sha256sum, and the stamp below needs one of them.
if command -v shasum >/dev/null; then HASH=(shasum -a 256)
elif command -v sha256sum >/dev/null; then HASH=(sha256sum)
else die "missing required tool: shasum or sha256sum"
fi

[[ -d ../../../libhoop ]] || die \
    "../../../libhoop is missing. sidecar's codecs are a private module; this
     stack builds them from a local checkout beside the repo (see
     sidecar/CLAUDE.md, and 'make libhoop-dev' at the repo root)."

# ------------------------------------------------------------------ 0. keys
#
# Four pieces of key material, and it is worth being clear about which does
# what before the walkthrough starts:
#
#   ca / ca.pub            the ONE trust decision. Every sidecar and the sshd
#                          bastion trust this public key and nothing else
#   *_host_key             each server's own identity, the file a real sshd
#                          would hold
#   user / user-cert.pub   the client. A key, plus a certificate the CA
#                          signed naming who it is and where it may log in
c_step "Certificate authority, host keys and one user certificate"

# certs.sh mints one certificate per attribute into here, while the stack is
# already up. The directory has to exist before compose up, because the
# client bind-mounts it.
mkdir -p keys/probe

# An 8h certificate outlives a working day and not a weekend, so "reuse the
# keys" has to mean "reuse the CA", not "reuse a certificate that expired
# overnight". Without this the stack comes up healthy and every login is
# refused with Permission denied, which reads like a broken build.
cert_expired() {
    [[ -f keys/user-cert.pub ]] || return 0
    local until epoch
    until=$(ssh-keygen -L -f keys/user-cert.pub 2>/dev/null |
            sed -n 's/.*Valid: from .* to \(.*\)$/\1/p')
    [[ -n "$until" ]] || return 0
    epoch=$(date -j -f '%Y-%m-%dT%H:%M:%S' "$until" +%s 2>/dev/null) ||
        epoch=$(date -d "${until/T/ }" +%s 2>/dev/null) || return 0
    (( epoch <= $(date +%s) ))
}

if [[ ! -f keys/ca ]]; then
    mkdir -p keys
    ssh-keygen -t ed25519 -f keys/ca              -N '' -q -C hoop-demo-ca
    ssh-keygen -t ed25519 -f keys/endhost_host_key -N '' -q -C endhost
    ssh-keygen -t ed25519 -f keys/bastion_host_key -N '' -q -C bastion
    ssh-keygen -t ed25519 -f keys/user            -N '' -q -C rider

    # -I is the key id, which this stack maps to the identity's SUBJECT: it
    #    is the name that lands in every audit record.
    # -n is the principals list, and it is the ONLY place the login names are
    #    decided. `devuser` is the account on the end-hop; `jump` is the
    #    login both bastions accept; `analyst` is a second account on the
    #    multi-user lane. A login name absent from this list is refused
    #    before any policy runs.
    #
    #    `ghost` is here ON PURPOSE and is NOT an account on any host in the
    #    stack. It is how the walkthrough shows that a certificate vouching
    #    for a name is not the same as that name existing: the cert admits
    #    the login, and the session is then refused for having no account.
    # -V bounds the certificate in time. Expiry is checked at the handshake.
    ssh-keygen -s keys/ca \
        -I rider@example.com \
        -n devuser,jump,analyst,ghost \
        -V +8h \
        keys/user.pub >/dev/null

    chmod 600 keys/user keys/*_host_key keys/ca
    c_ok "generated keys/ (CA, 2 host keys, 1 user certificate valid 8h)"
elif cert_expired; then
    # The CA and the host keys are still good; only the certificate ran out.
    # Re-signing is what a real deployment does too, and it keeps the
    # learned host keys in the client's known_hosts valid.
    ssh-keygen -s keys/ca \
        -I rider@example.com \
        -n devuser,jump,analyst,ghost \
        -V +8h \
        keys/user.pub >/dev/null
    c_ok "re-signed an expired user certificate (same CA, valid 8h)"
else
    c_ok "reusing keys/ (./run.sh down to regenerate)"
fi

printf '\n'
ssh-keygen -L -f keys/user-cert.pub | sed -n '2,12p' | sed 's/^/      /'

# ------------------------------------------------ 0a. break-glass identity
#
# A SECOND certificate, identical to the first except for its key id, which
# opa/policy.rego reads as input.context.subject and treats as the one
# identity allowed to run a flagged command.
#
# It exists to make the OPA demonstration land: the same command on the same
# lane comes back denied with one certificate and runs with the other, and
# the only difference between them is a field the CA signed. Minted every
# run, because it is short-lived on purpose.
cp keys/user.pub keys/probe/breakglass.pub
ssh-keygen -s keys/ca \
    -I incident-response@example.com \
    -n devuser \
    -V +8h \
    keys/probe/breakglass.pub >/dev/null
cp keys/user keys/probe/id
chmod 600 keys/probe/id
c_ok "minted keys/probe/breakglass-cert.pub (key id incident-response@example.com)"

# ------------------------------------------------- 0b. analyzer credential
#
# The sidecar takes a credentials_file — a PATH, never the key itself — and
# refuses a file readable by group or other. So the key has to land in a file
# whatever the operator prefers to hold it in, and the question is only which
# file.
#
# An environment variable is the nicer thing to TYPE and a file is the only
# thing the product READS, so run.sh bridges the two: export the variable,
# and the key is written to keys/anthropic.key at 0600. keys/ is gitignored
# and `./run.sh down` deletes it, so the credential neither reaches the
# repository nor outlives the stack.
#
# Without the variable the analyzer lane simply does not start. Everything
# else in the stack is unaffected, which is why this is a compose profile
# rather than a hard dependency.
WITH_AI=""
if [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
    printf '%s' "$ANTHROPIC_API_KEY" > keys/anthropic.key
    chmod 600 keys/anthropic.key
    WITH_AI=1
    c_ok "wrote keys/anthropic.key from \$ANTHROPIC_API_KEY (0600); the analyzer lane is in"
else
    rm -f keys/anthropic.key
    c_ok "no \$ANTHROPIC_API_KEY, so the analyzer lane stays down (export it and re-run)"
fi

# ---------------------------------------------------------------- 1. images
#
# The image carries the binary, so reusing one built from older sources is
# how a green ./demo.sh comes to prove nothing. Hash what goes INTO the image
# and compare it with the hash the image was built with: the sources decide
# whether to rebuild, not the operator's memory of what they edited.
#
# Everything both build contexts hold, because that is what docker copies in:
# there is no .dockerignore, so any file either tree gains or loses changes
# the image. Hashing only *.go would assume no build ever reads anything
# else, and nothing here enforces that.
#
# -H follows the two roots and nothing below them. ../../../libhoop is
# normally the symlink `make libhoop-dev` leaves, and a find without it walks
# no further than the link: the stamp then covers sidecar alone and holds
# still through every libhoop edit, which is the failure this check exists to
# stop. The count guards that, because a stamp over nothing looks exactly
# like a stamp over something unchanged.
source_stamp() {
    local ctx n
    for ctx in ../../../sidecar ../../../libhoop; do
        n=$(find -H "$ctx" -name .git -prune -o -type f -print0 | tr -cd '\0' | wc -c) || n=0
        (( n > 0 )) || die "the source stamp found no files under $ctx, so it
     cannot see a change there. Check that the path is a directory or a
     symlink to one."
    done
    {   find -H ../../../sidecar ../../../libhoop -name .git -prune -o -type f -print0 |
            LC_ALL=C sort -z | xargs -0 "${HASH[@]}"
        # Neither file is in either context, and both change the image.
        "${HASH[@]}" sidecar/Dockerfile docker-compose.yml
    } | "${HASH[@]}" | cut -d' ' -f1
}

image_stamp() {
    docker image inspect -f '{{index .Config.Labels "dev.hoop.source-stamp"}}' \
        hoop-inspect-ssh:local 2>/dev/null
}

c_step "Images"
STAMP=$(source_stamp)
if [[ -n "$REBUILD" ]]; then
    REASON="--rebuild"
elif ! docker image inspect hoop-inspect-ssh:local >/dev/null 2>&1; then
    REASON="there is no hoop-inspect-ssh:local yet"
elif [[ "$(image_stamp)" != "$STAMP" ]]; then
    REASON="../../../sidecar or ../../../libhoop changed since that image was built"
else
    REASON=""
fi

if [[ -n "$REASON" ]]; then
    printf '      %s\n' "$REASON"
    docker compose build --build-arg SOURCE_STAMP="$STAMP" endhost
    c_ok "built hoop-inspect-ssh:local from ../../../sidecar and ../../../libhoop"
else
    c_ok "reusing hoop-inspect-ssh:local; it holds ${STAMP:0:12}, which is these
      exact sources. ./run.sh --rebuild rebuilds it anyway."
fi
docker compose build bastion-sshd client >/dev/null
c_ok "built the sshd bastion and the client"

# --------------------------------------------------------------- 2. compose
c_step "Starting every end-hop, both bastions, OPA and the client"
if [[ -n "$WITH_AI" ]]; then
    docker compose --profile ai up -d --wait
else
    docker compose up -d --wait
fi
c_ok "endhost :2222, sidecar bastion :2223, sshd bastion :2224"
c_ok "endhost-multi :2225 (many accounts), :2226 (exec only)"
c_ok "endhost-opa :2228 (verdict from Rego)"
[[ -n "$WITH_AI" ]] && c_ok "endhost-ai :2229 (verdict from a model)"

# OPA's static image is distroless and carries no shell, so it gets no
# compose healthcheck. Poll it from a container that has curl instead: a lane
# with fail_open: false denies every statement until the policy is loaded,
# and a race here would read as a broken policy.
for _ in $(seq 30); do
    docker compose exec -T endhost-opa \
        curl -sf http://opa:8181/health >/dev/null 2>&1 && break
    sleep 1
done
docker compose exec -T endhost-opa curl -sf http://opa:8181/health >/dev/null 2>&1 \
    || die "opa did not become ready; docker compose logs opa"
c_ok "opa :18181 serving hoop.ssh from opa/policy.rego"

# What each lane RESOLVED to, straight from the process. The config file does
# not show this: an empty destination list looks like a complete bastion
# right up until the first forward is refused.
c_step "What each sidecar resolved to"
SVCS=(endhost endhost-multi endhost-opa bastion-sidecar)
[[ -n "$WITH_AI" ]] && SVCS+=(endhost-ai)
for svc in "${SVCS[@]}"; do
    printf '\n  \033[1m%s\033[0m\n' "$svc"
    docker compose exec -T "$svc" hoop-inspect -validate \
        -config /etc/hoop-inspect/config.yaml | sed 's/^/    /'
done

cat <<'EOF'

ready

  Three topologies, one certificate. Every command runs in the client
  container, and every one of them ends at the SAME end-hop.

    1. direct        docker compose exec client ssh direct id
    2. via sidecar   docker compose exec client ssh via-sidecar id
    3. via sshd      docker compose exec client ssh via-sshd id

  The enforcement is identical in all three, which is the claim:

    docker compose exec client ssh direct      cat data/secrets.env
    docker compose exec client ssh via-sidecar cat data/secrets.env
    docker compose exec client ssh via-sshd    cat data/secrets.env

  Users -- one listener, several accounts, and one the cert names but the
  host does not have:

    docker compose exec client ssh multi-devuser id      # uid 10001
    docker compose exec client ssh multi-analyst id      # uid 10004
    docker compose exec client ssh multi-ghost   id      # refused, and says why

  Capabilities -- a lane that admits exec and nothing else:

    docker compose exec client ssh     exec-only "echo ran"
    docker compose exec client ssh -T  exec-only            # shell refused
    docker compose exec client ssh -tt exec-only "echo x"   # pty refused

  Policy -- the verdict comes from Rego, not from the lane. The word match
  is local and free; what it MEANS for this certificate is decided at opa:

    docker compose exec client ssh opa-lane "id"                  # allowed
    docker compose exec client ssh opa-lane "curl https://x.test" # Rego denies
    docker compose exec client sftp -q opa-lane                   # writes refused

  Risk analysis -- a model reads the command, which is the only control here
  that sees through shell expansion (needs $ANTHROPIC_API_KEY at run time):

    docker compose exec client ssh ai-lane "uptime"
    docker compose exec client ssh ai-lane 'X=cat; $X /root/.aws/credentials'

  Walk it properly, one command at a time, with the explanations:

      README.md

  Or prove the whole thing at once:

      ./demo.sh

  Which certificate FIELD decided any of that -- one probe per attribute,
  each differing from a working certificate in exactly one place:

      ./certs.sh

  The audit trail, live:

      docker compose exec endhost tail -f /tmp/audit.jsonl
      docker compose logs -f endhost

  Teardown:
      ./run.sh down

EOF
